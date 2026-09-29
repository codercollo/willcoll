package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestPaySessionToken(t *testing.T) {
	p := newPayTestApp(t)
	now := time.Now()
	s := paySession{TenantID: testTenantID, UnitID: testUnitA, Phone: "+254722000001"}

	tok, err := p.app.signPaySession(s, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.app.verifyPaySession(tok, now)
	if err != nil || got.UnitID != testUnitA || got.TenantID != testTenantID || got.Phone != "+254722000001" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}

	payload, sig, _ := strings.Cut(tok, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"t":"` + testTenantID.String() + `","u":"` + uuid.NewString() + `","p":"x","e":99999999999}`))
	other := newPayTestApp(t)
	other.app.config.pay.sessionSecret = "a-different-secret"
	otherTok, _ := other.app.signPaySession(s, now.Add(time.Minute))

	for name, bad := range map[string]string{
		"payload swapped under a valid signature": forged + "." + sig,
		"signature altered":                       payload + "." + sig[:len(sig)-2] + "AA",
		"signed with another secret":              otherTok,
		"no signature":                            payload,
		"empty":                                   "",
		"garbage":                                 "not.a.token",
	} {
		if _, err := p.app.verifyPaySession(bad, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := p.app.verifyPaySession(tok, now.Add(2*time.Minute)); err == nil {
		t.Error("an expired token was accepted")
	}

	p.app.config.pay.sessionSecret = ""
	if _, err := p.app.verifyPaySession(tok, now); err == nil {
		t.Error("with no secret configured every token must be refused")
	}
}

func TestOTPHashBindsUnitAndPhone(t *testing.T) {
	p := newPayTestApp(t)
	base := p.app.otpHash(testUnitA, "+254722000001", "123456")
	for name, h := range map[string]string{
		"another code":  p.app.otpHash(testUnitA, "+254722000001", "123457"),
		"another unit":  p.app.otpHash(testUnitB, "+254722000001", "123456"),
		"another phone": p.app.otpHash(testUnitA, "+254722000002", "123456"),
	} {
		if h == base {
			t.Errorf("%s produced the same hash", name)
		}
	}
	if strings.Contains(base, "123456") {
		t.Error("the hash must not contain the code")
	}
}

func TestPayEndpointsAreOffWithoutASecret(t *testing.T) {
	p := newPayTestApp(t)
	p.app.config.pay.sessionSecret = ""
	for name, h := range map[string]gin.HandlerFunc{
		"page": p.app.showPayPageHandler, "otp": p.app.requestPayOTPHandler,
		"verify": p.app.verifyPayOTPHandler, "balances": p.app.showPayBalancesHandler,
		"intent": p.app.createPaymentIntentHandler, "status": p.app.showPaymentIntentStatusHandler,
	} {
		if w := do(h, "POST", "/", `{}`, payParams("runda", "A1"), nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", name, w.Code)
		}
	}
}

func TestShowPayPage(t *testing.T) {
	t.Run("shows the unit and nothing about the tenant", func(t *testing.T) {
		p := newPayTestApp(t)
		p.expectTarget()
		prop := testPropertyRow()
		ch := "CH-1"
		prop.PayheroChannelID = &ch
		p.q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA, UnitCode: "A1"}, nil)
		p.q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(prop, nil)

		w := do(p.app.showPayPageHandler, "GET", "/", "", payParams("runda", "A1"), nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"unit_code": "A1"`) || !strings.Contains(w.Body.String(), `"can_pay": true`) {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		for _, leak := range []string{"tenant", "phone", "balance"} {
			if strings.Contains(strings.ToLower(w.Body.String()), leak) {
				t.Errorf("the public page leaks %q", leak)
			}
		}
	})
	t.Run("unknown slug or unit is a 404", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().ResolvePayTarget(gomock.Any(), gomock.Any()).Return(sqlc.ResolvePayTargetRow{}, sql.ErrNoRows)
		if w := do(p.app.showPayPageHandler, "GET", "/", "", payParams("nope", "Z9"), nil); w.Code != http.StatusNotFound {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestRequestPayOTP(t *testing.T) {
	const phone = "+254722000001"
	codeRE := regexp.MustCompile(`^\d{6}$`)

	issue := func(t *testing.T, p *payTestApp, listed []string, recent int32, body string) (code int, resp string) {
		p.expectTarget()
		p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return(listed, nil)
		w := do(p.app.requestPayOTPHandler, "POST", "/", body, payParams("runda", "A1"), nil)
		p.app.wg.Wait()
		return w.Code, w.Body.String()
	}

	t.Run("a phone on the lease gets a code, stored only as a keyed hash", func(t *testing.T) {
		p := newPayTestApp(t)
		var storedHash string
		p.q.EXPECT().CountRecentPayOTPs(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CreatePayOTP(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreatePayOTPParams) error {
				storedHash = a.CodeHash
				if a.Phone != phone || a.UnitID != testUnitA {
					t.Errorf("stored %+v", a)
				}
				return nil
			})
		p.q.EXPECT().PurgePayOTPs(gomock.Any(), gomock.Any()).Return(nil)

		code, body := issue(t, p, []string{"0722 000 001"}, 0, `{"phone":"0722000001"}`)
		if code != http.StatusAccepted {
			t.Fatalf("status %d: %s", code, body)
		}
		if len(p.sms.otps) != 1 || p.sms.otps[0].Phone != phone || !codeRE.MatchString(p.sms.otps[0].Code) {
			t.Fatalf("SMS sent = %+v", p.sms.otps)
		}
		sent := p.sms.otps[0].Code
		if storedHash == sent || storedHash != p.app.otpHash(testUnitA, phone, sent) {
			t.Errorf("stored hash %q does not match the keyed hash of the code sent", storedHash)
		}
	})

	t.Run("an unknown phone gets the same answer and no text", func(t *testing.T) {
		known := newPayTestApp(t)
		known.q.EXPECT().CountRecentPayOTPs(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		known.q.EXPECT().CreatePayOTP(gomock.Any(), gomock.Any()).Return(nil)
		known.q.EXPECT().PurgePayOTPs(gomock.Any(), gomock.Any()).Return(nil)
		_, knownBody := issue(t, known, []string{phone}, 0, `{"phone":"`+phone+`"}`)

		p := newPayTestApp(t)
		code, body := issue(t, p, []string{phone}, 0, `{"phone":"0799999999"}`)
		if code != http.StatusAccepted || body != knownBody {
			t.Errorf("unknown phone answer differs (%d): %s\nvs %s", code, body, knownBody)
		}
		if len(p.sms.otps) != 0 {
			t.Error("a text was sent to a number not on the lease")
		}
	})

	t.Run("too many codes for one phone is a 429", func(t *testing.T) {
		p := newPayTestApp(t)
		p.q.EXPECT().CountRecentPayOTPs(gomock.Any(), gomock.Any()).Return(int32(3), nil)
		code, _ := issue(t, p, []string{phone}, 3, `{"phone":"`+phone+`"}`)
		if code != http.StatusTooManyRequests || len(p.sms.otps) != 0 {
			t.Errorf("status %d, texts %d", code, len(p.sms.otps))
		}
	})

	t.Run("a phone is required", func(t *testing.T) {
		p := newPayTestApp(t)
		p.expectTarget()
		if w := do(p.app.requestPayOTPHandler, "POST", "/", `{}`, payParams("runda", "A1"), nil); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestVerifyPayOTP(t *testing.T) {
	const phone = "+254722000001"
	otpRow := func(p *payTestApp, code string, attempts int32, expires time.Duration, consumed bool) sqlc.PayOtp {
		row := sqlc.PayOtp{ID: uuid.New(), CodeHash: p.app.otpHash(testUnitA, phone, code), Attempts: attempts, ExpiresAt: time.Now().Add(expires)}
		if consumed {
			now := time.Now()
			row.ConsumedAt = &now
		}
		return row
	}
	verify := func(p *payTestApp, code string) (int, string) {
		p.expectTarget()
		w := do(p.app.verifyPayOTPHandler, "POST", "/", `{"phone":"0722000001","code":"`+code+`"}`, payParams("runda", "A1"), nil)
		return w.Code, w.Body.String()
	}

	t.Run("the right code returns a session for this unit only", func(t *testing.T) {
		p := newPayTestApp(t)
		row := otpRow(p, "482913", 0, time.Minute, false)
		p.q.EXPECT().GetLatestPayOTP(gomock.Any(), gomock.Any()).Return(row, nil)
		p.q.EXPECT().BumpPayOTPAttempts(gomock.Any(), gomock.Any()).Return(nil)
		p.q.EXPECT().ConsumePayOTP(gomock.Any(), gomock.Any()).Return(int64(1), nil)

		code, body := verify(p, "482913")
		if code != http.StatusOK {
			t.Fatalf("status %d: %s", code, body)
		}
		var out struct{ Token string }
		var raw map[string]json.RawMessage
		_ = json.Unmarshal([]byte(body), &raw)
		_ = json.Unmarshal(raw["token"], &out.Token)
		s, err := p.app.verifyPaySession(out.Token, time.Now())
		if err != nil || s.UnitID != testUnitA || s.TenantID != testTenantID || s.Phone != phone {
			t.Errorf("session = %+v, %v", s, err)
		}
	})

	rejected := map[string]struct {
		row   func(p *payTestApp) sqlc.PayOtp
		err   error
		bump  bool
		guess string
	}{
		"wrong code counts as an attempt": {func(p *payTestApp) sqlc.PayOtp { return otpRow(p, "482913", 0, time.Minute, false) }, nil, true, "000000"},
		"expired":                         {func(p *payTestApp) sqlc.PayOtp { return otpRow(p, "482913", 0, -time.Minute, false) }, nil, false, "482913"},
		"already used":                    {func(p *payTestApp) sqlc.PayOtp { return otpRow(p, "482913", 0, time.Minute, true) }, nil, false, "482913"},
		"locked after five attempts":      {func(p *payTestApp) sqlc.PayOtp { return otpRow(p, "482913", 5, time.Minute, false) }, nil, false, "482913"},
		"no code was ever issued":         {func(p *payTestApp) sqlc.PayOtp { return sqlc.PayOtp{} }, sql.ErrNoRows, false, "482913"},
	}
	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			p := newPayTestApp(t)
			p.q.EXPECT().GetLatestPayOTP(gomock.Any(), gomock.Any()).Return(tc.row(p), tc.err)
			if tc.bump {
				p.q.EXPECT().BumpPayOTPAttempts(gomock.Any(), gomock.Any()).Return(nil)
			}
			code, body := verify(p, tc.guess)
			if code != http.StatusUnauthorized || strings.Contains(body, "token") {
				t.Errorf("status %d, body %s: every failure must be the same 401 with no token", code, body)
			}
		})
	}
}

func TestPayBalances(t *testing.T) {
	balances := []sqlc.ListUnitBalancesRow{
		{Type: "RENT", Balance: money("5000")}, {Type: "WATER", Balance: money("0")},
		{Type: "GARBAGE", Balance: money("300")}, {Type: "RENT_DEPOSIT", Balance: money("0")},
		{Type: "WATER_DEPOSIT", Balance: money("2000")},
	}

	t.Run("no session, another unit or an expired session is a 401", func(t *testing.T) {
		p := newPayTestApp(t)
		p.expectTarget()
		for name, h := range map[string]map[string]string{
			"no token":     nil,
			"another unit": p.bearer(t, testUnitB, "+254700000000", time.Minute),
			"expired":      p.bearer(t, testUnitA, "+254700000000", -time.Minute),
			"not a token":  {"Authorization": "Bearer abc.def"},
		} {
			if w := do(p.app.showPayBalancesHandler, "GET", "/", "", payParams("runda", "A1"), h); w.Code != http.StatusUnauthorized {
				t.Errorf("%s = %d, want 401", name, w.Code)
			}
		}
	})

	t.Run("deposits always shown (paid ones unpayable); garbage only where billed", func(t *testing.T) {
		p := newPayTestApp(t)
		p.expectTarget()
		prop := testPropertyRow()
		prop.GarbageEnabled = false
		p.q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(prop, nil)
		p.q.EXPECT().ListUnitBalances(gomock.Any(), gomock.Any()).Return(balances, nil)

		w := do(p.app.showPayBalancesHandler, "GET", "/", "", payParams("runda", "A1"), p.bearer(t, testUnitA, "+254722000001", time.Minute))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		body := w.Body.String()
		// RENT_DEPOSIT is at 0.00: still shown (a settled deposit doesn't
		// disappear), but not payable — a deposit can't be paid ahead.
		for _, want := range []string{`"type": "RENT"`, `"type": "WATER"`, `"type": "WATER_DEPOSIT"`, `"balance": "2000.00"`, `"type": "RENT_DEPOSIT"`} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %s in %s", want, body)
			}
		}
		if strings.Contains(body, `"type": "GARBAGE"`) {
			t.Error("GARBAGE must not be offered when the property doesn't bill it")
		}

		var resp struct {
			Balances []struct {
				Type    string `json:"type"`
				Balance string `json:"balance"`
				Payable bool   `json:"payable"`
			} `json:"balances"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, l := range resp.Balances {
			if l.Type == "RENT_DEPOSIT" && l.Payable {
				t.Error("a settled RENT_DEPOSIT (0.00) must not be payable")
			}
			if l.Type == "WATER_DEPOSIT" && !l.Payable {
				t.Error("an outstanding WATER_DEPOSIT (2000.00) must be payable")
			}
		}
	})
}

func TestCreatePaymentIntent(t *testing.T) {
	channel := "CH-1"
	linked := func() sqlc.Property {
		p := testPropertyRow()
		p.PayheroChannelID = &channel
		p.GarbageEnabled = true
		return p
	}
	balances := []sqlc.ListUnitBalancesRow{
		{Type: "RENT", Balance: money("5000")}, {Type: "GARBAGE", Balance: money("300")}, {Type: "RENT_DEPOSIT", Balance: money("0")},
	}
	post := func(p *payTestApp, body string) (int, string) {
		p.expectTarget()
		w := do(p.app.createPaymentIntentHandler, "POST", "/", body, payParams("runda", "A1"), p.bearer(t, testUnitA, "+254722000001", time.Minute))
		return w.Code, w.Body.String()
	}
	expectLoad := func(p *payTestApp, prop sqlc.Property) {
		p.q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(prop, nil).AnyTimes()
		p.q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA, UnitCode: "A1"}, nil).AnyTimes()
		p.q.EXPECT().ListUnitBalances(gomock.Any(), gomock.Any()).Return(balances, nil).AnyTimes()
		// No pending intent to reuse in these tests: every one always mints a fresh one.
		p.q.EXPECT().GetPendingIntentForUnit(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{}, sql.ErrNoRows).AnyTimes()
	}

	t.Run("stores the split and asks PayHero to prompt the phone", func(t *testing.T) {
		p := newPayTestApp(t)
		expectLoad(p, linked())
		intentID := uuid.New()
		p.q.EXPECT().CreatePaymentIntent(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreatePaymentIntentParams) (sqlc.PaymentIntent, error) {
				if !strings.HasPrefix(a.ExternalReference, "WC-") || a.Phone != "+254722000001" || a.Status != "pending" {
					t.Errorf("intent = %+v", a)
				}
				if !strings.Contains(string(a.Lines), `"RENT"`) || !strings.Contains(string(a.Lines), `"5000.00"`) {
					t.Errorf("lines = %s", a.Lines)
				}
				return sqlc.PaymentIntent{ID: intentID, ExternalReference: a.ExternalReference}, nil
			})
		p.q.EXPECT().SetPaymentIntentCheckout(gomock.Any(), gomock.Any()).Return(nil)

		code, body := post(p, `{"lines":[{"type":"rent","amount":"5000.00"},{"type":"GARBAGE","amount":"300"}]}`)
		if code != http.StatusAccepted || !strings.Contains(body, intentID.String()) {
			t.Fatalf("status %d: %s", code, body)
		}
		if len(p.push.calls) != 1 {
			t.Fatalf("STK pushes = %d", len(p.push.calls))
		}
		c := p.push.calls[0]
		if c.Amount != 5300 || c.PhoneNumber != "254722000001" || c.ChannelID != "CH-1" || !strings.HasPrefix(c.ExternalReference, "WC-") {
			t.Errorf("push = %+v (whole shillings, international phone, the property channel)", c)
		}
		if c.CallbackURL != "https://api.example.com/v1/webhooks/payhero/collections?token="+testWebhookSecret {
			t.Errorf("callback = %q", c.CallbackURL)
		}
		if strings.Contains(body, "CH-1") {
			t.Error("the channel id must not reach the client")
		}
	})

	t.Run("a failed push fails the intent and hides PayHero details", func(t *testing.T) {
		p := newPayTestApp(t)
		expectLoad(p, linked())
		p.push.err = &payhero.APIError{StatusCode: 422, Body: `{"error":"channel 9931 has insufficient balance"}`}
		p.q.EXPECT().CreatePaymentIntent(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{ID: uuid.New()}, nil)
		p.q.EXPECT().SetPaymentIntentStatus(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.SetPaymentIntentStatusParams) error {
				if a.Status != "failed" {
					t.Errorf("intent status %q, want failed", a.Status)
				}
				return nil
			})
		code, body := post(p, `{"lines":[{"type":"RENT","amount":"100"}]}`)
		if code != http.StatusBadGateway || strings.Contains(body, "9931") || strings.Contains(body, "insufficient") {
			t.Errorf("status %d, body %s", code, body)
		}
	})

	t.Run("a timed-out push is left pending, not told to the tenant as a failure", func(t *testing.T) {
		p := newPayTestApp(t)
		expectLoad(p, linked())
		p.push.err = fmt.Errorf("%w: %w", payhero.ErrUnavailable, context.DeadlineExceeded)
		intentID := uuid.New()
		p.q.EXPECT().CreatePaymentIntent(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{ID: intentID}, nil)
		// Crucially, no SetPaymentIntentStatus("failed") call: the push may
		// have already reached PayHero, so the intent stays pending.

		code, body := post(p, `{"lines":[{"type":"RENT","amount":"100"}]}`)
		if code != http.StatusAccepted {
			t.Fatalf("status %d, body %s", code, body)
		}
		if !strings.Contains(body, "try again") || strings.Contains(body, "failed") {
			t.Errorf("body = %s, want the ambiguous retry message, not a failure", body)
		}
	})

	t.Run("PayHero not configured is a 503", func(t *testing.T) {
		p := newPayTestApp(t)
		expectLoad(p, linked())
		p.push.err = payhero.ErrNotConfigured
		p.q.EXPECT().CreatePaymentIntent(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{ID: uuid.New()}, nil)
		p.q.EXPECT().SetPaymentIntentStatus(gomock.Any(), gomock.Any()).Return(nil)
		if code, _ := post(p, `{"lines":[{"type":"RENT","amount":"100"}]}`); code != http.StatusServiceUnavailable {
			t.Errorf("status %d", code)
		}
	})

	rejected := map[string]struct {
		body string
		prop func() sqlc.Property
		want string
	}{
		"cents cannot go through STK":         {`{"lines":[{"type":"RENT","amount":"100.50"}]}`, linked, "whole number of shillings"},
		"a deposit with nothing owed":         {`{"lines":[{"type":"RENT_DEPOSIT","amount":"100"}]}`, linked, "cannot be paid here"},
		"garbage where it is not billed":      {`{"lines":[{"type":"GARBAGE","amount":"100"}]}`, func() sqlc.Property { p := linked(); p.GarbageEnabled = false; return p }, "cannot be paid here"},
		"a made-up ledger":                    {`{"lines":[{"type":"TAX","amount":"100"}]}`, linked, "cannot be paid here"},
		"the same line twice":                 {`{"lines":[{"type":"RENT","amount":"1"},{"type":"RENT","amount":"2"}]}`, linked, "appears twice"},
		"over the M-Pesa limit":               {`{"lines":[{"type":"RENT","amount":"250001"}]}`, linked, "not be more than"},
		"zero":                                {`{"lines":[{"type":"RENT","amount":"0"}]}`, linked, "greater than zero"},
		"a property not linked to PayHero":    {`{"lines":[{"type":"RENT","amount":"100"}]}`, func() sqlc.Property { p := testPropertyRow(); return p }, "not set up to take payments"},
		"no lines":                            {`{"lines":[]}`, linked, "at least one"},
		"a phone that is not a Kenyan mobile": {`{"phone":"12345","lines":[{"type":"RENT","amount":"100"}]}`, linked, "Kenyan mobile"},
	}
	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			p := newPayTestApp(t)
			expectLoad(p, tc.prop())
			code, body := post(p, tc.body)
			if code != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
				t.Errorf("status %d, body %s (want 422 containing %q)", code, body, tc.want)
			}
			if len(p.push.calls) != 0 {
				t.Error("an invalid intent must never reach PayHero")
			}
		})
	}
}

func TestPaymentIntentStatus(t *testing.T) {
	get := func(p *payTestApp, headers map[string]string) (int, string) {
		w := do(p.app.showPaymentIntentStatusHandler, "GET", "/", "", gin.Params{{Key: "id", Value: testEntryID.String()}}, headers)
		return w.Code, w.Body.String()
	}

	t.Run("reports the status to the payer of that unit", func(t *testing.T) {
		p := newPayTestApp(t)
		p.q.EXPECT().ExpireStaleIntents(gomock.Any(), gomock.Any()).Return(int64(0), nil)
		reason := "cancelled by customer"
		p.q.EXPECT().GetPaymentIntent(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{UnitID: testUnitA, Status: "failed", FailureReason: &reason}, nil)
		code, body := get(p, p.bearer(t, testUnitA, "+254722000001", time.Minute))
		if code != http.StatusOK || !strings.Contains(body, `"status": "failed"`) || !strings.Contains(body, "cancelled by customer") {
			t.Errorf("status %d: %s", code, body)
		}
	})
	t.Run("another unit's intent reads as not found", func(t *testing.T) {
		p := newPayTestApp(t)
		p.q.EXPECT().ExpireStaleIntents(gomock.Any(), gomock.Any()).Return(int64(0), nil)
		p.q.EXPECT().GetPaymentIntent(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{UnitID: testUnitB, Status: "pending"}, nil)
		if code, _ := get(p, p.bearer(t, testUnitA, "+254722000001", time.Minute)); code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})
	t.Run("needs a session", func(t *testing.T) {
		p := newPayTestApp(t)
		if code, _ := get(p, nil); code != http.StatusUnauthorized {
			t.Errorf("status %d, want 401", code)
		}
	})
}
