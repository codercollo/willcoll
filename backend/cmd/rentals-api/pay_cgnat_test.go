package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// payRouter mounts the real pay handlers behind the real pay limiters, with
// the production limits, on a bare engine.
func payRouter(p *payTestApp) *gin.Engine {
	gin.SetMode(gin.TestMode)
	p.app.config.limiter.enabled = true
	p.app.config.pay.ipRPS, p.app.config.pay.ipBurst = 20, 40
	p.app.config.pay.statusRPS, p.app.config.pay.statusBurst = 1, 3
	perIP, perIntent := p.app.payRateLimits()
	r := gin.New()
	g := r.Group("/pay", perIP)
	g.POST("/:propertySlug/:unitCode/intent", p.app.createPaymentIntentHandler)
	g.GET("/intents/:id/status", perIntent, p.app.showPaymentIntentStatusHandler)
	return r
}

// Carrier-grade NAT: many tenants behind one public IP must not trip the pay
// limiters. 20 different pay tokens from ONE IP each create an intent and poll
// its status concurrently, and none gets a 429.
func TestPayLimitsDoNotStarveSharedIP(t *testing.T) {
	p := newPayTestApp(t)
	p.expectTarget()
	channel := "CH-1"
	prop := testPropertyRow()
	prop.PayheroChannelID = &channel
	prop.GarbageEnabled = true
	p.q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(prop, nil).AnyTimes()
	p.q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA, UnitCode: "A1"}, nil).AnyTimes()
	p.q.EXPECT().ListUnitBalances(gomock.Any(), gomock.Any()).
		Return([]sqlc.ListUnitBalancesRow{{Type: "RENT", Balance: money("5000")}}, nil).AnyTimes()
	p.q.EXPECT().GetPendingIntentForUnit(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{}, sql.ErrNoRows).AnyTimes()
	p.q.EXPECT().CreatePaymentIntent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.CreatePaymentIntentParams) (sqlc.PaymentIntent, error) {
			return sqlc.PaymentIntent{ID: uuid.New(), ExternalReference: a.ExternalReference}, nil
		}).AnyTimes()
	p.q.EXPECT().SetPaymentIntentCheckout(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	p.q.EXPECT().ExpireStaleIntents(gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	p.q.EXPECT().GetPaymentIntent(gomock.Any(), gomock.Any()).
		Return(sqlc.PaymentIntent{UnitID: testUnitA, Status: "pending"}, nil).AnyTimes()

	const payers = 20
	phones := make([]string, payers)
	for i := range phones {
		phones[i] = fmt.Sprintf("+2547220000%02d", i)
	}
	p.q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).Return(sqlc.Lease{ID: testLeaseID}, nil).AnyTimes()
	p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return(phones, nil).AnyTimes()

	router := payRouter(p)
	var wg sync.WaitGroup
	codes := make(chan string, payers*2)
	for i := range payers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := p.app.signPaySession(paySession{TenantID: testTenantID, UnitID: testUnitA, LeaseID: testLeaseID, Phone: phones[i]}, time.Now().Add(time.Minute))
			if err != nil {
				codes <- err.Error()
				return
			}
			auth := "Bearer " + tok

			req := httptest.NewRequest(http.MethodPost, "/pay/runda/A1/intent", strings.NewReader(`{"lines":[{"type":"RENT","amount":"100"}]}`))
			req.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusAccepted {
				codes <- fmt.Sprintf("payer %d create = %d: %s", i, w.Code, w.Body)
				return
			}
			var out struct {
				Intent struct {
					ID string `json:"id"`
				} `json:"intent"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				codes <- err.Error()
				return
			}

			req = httptest.NewRequest(http.MethodGet, "/pay/intents/"+out.Intent.ID+"/status", nil)
			req.Header.Set("Authorization", auth)
			w = httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				codes <- fmt.Sprintf("payer %d status = %d: %s", i, w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	close(codes)
	for msg := range codes {
		t.Error(msg)
	}
}

// Status polling is bucketed per intent: one intent polled in a tight loop is
// cut off (burst 3), while a different intent from the same IP is unaffected.
func TestPayStatusPollingIsLimitedPerIntent(t *testing.T) {
	p := newPayTestApp(t)
	p.q.EXPECT().ExpireStaleIntents(gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	p.q.EXPECT().GetPaymentIntent(gomock.Any(), gomock.Any()).
		Return(sqlc.PaymentIntent{UnitID: testUnitA, Status: "pending"}, nil).AnyTimes()
	h := p.bearer(t, testUnitA, "+254722000001", time.Minute)
	router := payRouter(p)

	poll := func(id uuid.UUID) int {
		req := httptest.NewRequest(http.MethodGet, "/pay/intents/"+id.String()+"/status", nil)
		req.Header.Set("Authorization", h["Authorization"])
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	first, second := uuid.New(), uuid.New()
	for i := 1; i <= 3; i++ {
		if code := poll(first); code != http.StatusOK {
			t.Fatalf("poll %d = %d, want 200", i, code)
		}
	}
	if code := poll(first); code != http.StatusTooManyRequests {
		t.Errorf("4th immediate poll of one intent = %d, want 429", code)
	}
	if code := poll(second); code != http.StatusOK {
		t.Errorf("another intent from the same IP = %d, want 200", code)
	}
}

// A firm that has had its daily allowance of codes texted gets 429 for further
// codes, the System page is told, and no SMS is sent.
func TestPayOTPFirmDailyCeiling(t *testing.T) {
	const phone = "+254722000001"
	p := newPayTestApp(t)
	p.app.models.PayAccess.FirmDailyOTPs = 500
	p.expectTarget()
	p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{phone}, nil).AnyTimes()
	p.q.EXPECT().CountRecentPayOTPs(gomock.Any(), gomock.Any()).Return(int32(0), nil).AnyTimes()
	p.q.EXPECT().CountPayOTPsSince(gomock.Any(), gomock.Any()).Return(int32(500), nil)

	w := do(p.app.requestPayOTPHandler, "POST", "/", `{"phone":"0722000001"}`, payParams("runda", "A1"), nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := p.app.otpCeilings.list(time.Now()); len(got) != 1 || got[0]["tenant_id"] != testTenantID {
		t.Errorf("ceiling firms = %v, want the one firm", got)
	}

	t.Run("below the ceiling a code is issued", func(t *testing.T) {
		p := newPayTestApp(t)
		p.app.models.PayAccess.FirmDailyOTPs = 500
		p.expectTarget()
		p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{phone}, nil).AnyTimes()
		p.q.EXPECT().CountRecentPayOTPs(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CountPayOTPsSince(gomock.Any(), gomock.Any()).Return(int32(499), nil)
		p.q.EXPECT().CreatePayOTP(gomock.Any(), gomock.Any()).Return(nil)
		p.q.EXPECT().PurgePayOTPs(gomock.Any(), gomock.Any()).Return(nil)
		if w := do(p.app.requestPayOTPHandler, "POST", "/", `{"phone":"0722000001"}`, payParams("runda", "A1"), nil); w.Code != http.StatusAccepted {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
}
