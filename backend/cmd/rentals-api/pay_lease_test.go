package main

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// C3.1: a pay-session token is confined to the lease it was minted for. It
// stops working when that lease is terminated (move-out), when its end date has
// passed, when another lease now holds the unit, or when the payer's phone is
// no longer on it, on every pay route, not just at token expiry.
func TestPaySessionDiesWithItsLease(t *testing.T) {
	const phone = "+254722000001"
	yesterday := time.Now().AddDate(0, 0, -2)
	tomorrow := time.Now().AddDate(0, 0, 2)

	cases := map[string]func(p *payTestApp){
		"lease terminated (moved out)": func(p *payTestApp) {
			p.q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).Return(sqlc.Lease{}, sql.ErrNoRows).AnyTimes()
		},
		"lease end date passed": func(p *payTestApp) {
			p.q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).
				Return(sqlc.Lease{ID: testLeaseID, EndDate: &yesterday}, nil).AnyTimes()
			p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{phone}, nil).AnyTimes()
		},
		"a new lease now holds the unit": func(p *payTestApp) {
			p.q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).
				Return(sqlc.Lease{ID: uuid.New()}, nil).AnyTimes()
			p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{phone}, nil).AnyTimes()
		},
		"payer phone no longer on the lease": func(p *payTestApp) {
			p.q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).Return(sqlc.Lease{ID: testLeaseID}, nil).AnyTimes()
			p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{"+254799999999"}, nil).AnyTimes()
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPayTestApp(t)
			p.expectTarget()
			setup(p)
			tok, err := p.app.signPaySession(paySession{TenantID: testTenantID, UnitID: testUnitA, LeaseID: testLeaseID, Phone: phone}, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			h := map[string]string{"Authorization": "Bearer " + tok}
			params := payParams("runda", "A1")
			statusParams := gin.Params{{Key: "id", Value: uuid.NewString()}}

			for route, w := range map[string]int{
				"balances": do(p.app.showPayBalancesHandler, "GET", "/", "", params, h).Code,
				"intent":   do(p.app.createPaymentIntentHandler, "POST", "/", `{"lines":[{"type":"RENT","amount":"100"}]}`, params, h).Code,
				"status":   do(p.app.showPaymentIntentStatusHandler, "GET", "/", "", statusParams, h).Code,
			} {
				if w != http.StatusUnauthorized {
					t.Errorf("%s = %d, want 401", route, w)
				}
			}
			if len(p.push.calls) != 0 {
				t.Error("an STK push was sent on a dead lease")
			}
		})
	}

	t.Run("a lease ending in the future still works", func(t *testing.T) {
		p := newPayTestApp(t)
		p.expectTarget()
		p.q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).
			Return(sqlc.Lease{ID: testLeaseID, EndDate: &tomorrow}, nil).AnyTimes()
		p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{phone}, nil).AnyTimes()
		p.q.EXPECT().ListUnitBalances(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		p.q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil).AnyTimes()
		tok, _ := p.app.signPaySession(paySession{TenantID: testTenantID, UnitID: testUnitA, LeaseID: testLeaseID, Phone: phone}, time.Now().Add(time.Minute))
		h := map[string]string{"Authorization": "Bearer " + tok}
		if w := do(p.app.showPayBalancesHandler, "GET", "/", "", payParams("runda", "A1"), h); w.Code == http.StatusUnauthorized {
			t.Fatalf("a live lease was refused: %d %s", w.Code, w.Body)
		}
	})
}

// Unit A's token cannot create an intent on unit B's pay URL: 404, no STK push.
func TestPayIntentWithAnotherUnitsTokenIs404(t *testing.T) {
	p := newPayTestApp(t)
	p.expectTarget() // the URL resolves to unit A
	h := p.bearer(t, testUnitB, "+254722000001", time.Minute)
	w := do(p.app.createPaymentIntentHandler, "POST", "/", `{"lines":[{"type":"RENT","amount":"100"}]}`, payParams("runda", "A1"), h)
	if w.Code != http.StatusNotFound || len(p.push.calls) != 0 {
		t.Fatalf("status %d, pushes %d: %s", w.Code, len(p.push.calls), w.Body)
	}
}

// A token minted for one lease cannot be re-pointed at another by editing it:
// the lease id is inside the signed payload.
func TestPaySessionLeaseIDIsSigned(t *testing.T) {
	p := newPayTestApp(t)
	tok, _ := p.app.signPaySession(paySession{TenantID: testTenantID, UnitID: testUnitA, LeaseID: testLeaseID, Phone: "+254722000001"}, time.Now().Add(time.Minute))
	s, err := p.app.verifyPaySession(tok, time.Now())
	if err != nil || s.LeaseID != testLeaseID {
		t.Fatalf("round trip = %+v, %v", s, err)
	}
}

func TestPayOTPRequestsLimitedPerIP(t *testing.T) {
	p := newPayTestApp(t)
	p.expectTarget()
	p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	send := func() int {
		return do(p.app.requestPayOTPHandler, "POST", "/", `{"phone":"0722000001"}`, payParams("runda", "A1"), nil).Code
	}
	for i := 1; i <= payOTPIPMax; i++ {
		if code := send(); code != http.StatusAccepted {
			t.Fatalf("request %d = %d, want 202", i, code)
		}
	}
	if code := send(); code != http.StatusTooManyRequests {
		t.Fatalf("request over the cap = %d, want 429", code)
	}
}

func TestIPWindowLimiter(t *testing.T) {
	var l ipWindowLimiter
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow("1.1.1.1", now, 3, time.Hour) {
			t.Fatalf("hit %d refused", i)
		}
	}
	if l.allow("1.1.1.1", now, 3, time.Hour) {
		t.Error("4th hit allowed")
	}
	if !l.allow("2.2.2.2", now, 3, time.Hour) {
		t.Error("another IP shares the first one's budget")
	}
	if !l.allow("1.1.1.1", now.Add(time.Hour+time.Second), 3, time.Hour) {
		t.Error("the window never reopens")
	}
}
