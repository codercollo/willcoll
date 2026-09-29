package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/internal/sms"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

const (
	testWebhookSecret = "hook-secret"
	testPaySecret     = "pay-secret-for-tests"
)

// fakePush records STK pushes instead of calling PayHero.
type fakePush struct {
	mu    sync.Mutex
	calls []payhero.STKPushRequest
	err   error
	resp  *payhero.STKPushResponse

	registerCalls []payhero.RegisterChannelRequest
	registerErr   error
	registerResp  *payhero.Channel
	listErr       error
	listResp      []payhero.Channel
}

func (f *fakePush) STKPush(_ context.Context, req payhero.STKPushRequest) (*payhero.STKPushResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &payhero.STKPushResponse{Success: true, Status: "QUEUED", CheckoutRequestID: "ws_CO_test"}, nil
}

func (f *fakePush) RegisterChannel(_ context.Context, req payhero.RegisterChannelRequest) (*payhero.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls = append(f.registerCalls, req)
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	if f.registerResp != nil {
		return f.registerResp, nil
	}
	return &payhero.Channel{ID: 90001, ChannelType: req.ChannelType, ShortCode: req.ShortCode, AccountNumber: req.AccountNumber, IsActive: true}, nil
}

func (f *fakePush) ListChannels(_ context.Context) ([]payhero.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}

// fakeSMS records the texts that would have been sent.
type fakeSMS struct {
	mu            sync.Mutex
	otps          []fakeOTP
	confirmations []sms.PaymentConfirmation
	confirmPhones []string
	err           error
}

type fakeOTP struct{ Phone, Code string }

func (f *fakeSMS) SendOTP(_ context.Context, phone, code string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.otps = append(f.otps, fakeOTP{phone, code})
	return f.err
}

func (f *fakeSMS) SendPaymentConfirmation(_ context.Context, phone string, p sms.PaymentConfirmation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmations = append(f.confirmations, p)
	f.confirmPhones = append(f.confirmPhones, phone)
	return f.err
}

type payTestApp struct {
	app   *application
	store *mock.MockStore
	q     *mock.MockQuerier
	push  *fakePush
	sms   *fakeSMS
}

// newPayTestApp returns an app with mocked storage (the cross-tenant lookups
// on the store, tenant work on the querier) and fake PayHero and SMS clients.
func newPayTestApp(t *testing.T) *payTestApp {
	t.Helper()
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()
	store.EXPECT().ExecTenantTxExclusive(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, _ string, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()

	push, sms := &fakePush{}, &fakeSMS{}
	app := &application{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:  data.NewModelsFromStore(store, 0),
		payhero: push,
		sms:     sms,
	}
	app.config.env = "development"
	app.models.Billing.Env = app.config.env
	app.config.payhero.webhookSecret = testWebhookSecret
	app.config.payhero.billingChannelID = "BILLING-CH"
	app.config.payhero.callbackBaseURL = "https://api.example.com"
	app.config.pay.sessionSecret = testPaySecret
	app.config.pay.otpTTL = 5 * time.Minute
	app.config.pay.sessionTTL = 15 * time.Minute
	app.config.pay.intentTTL = 10 * time.Minute
	return &payTestApp{app: app, store: store, q: q, push: push, sms: sms}
}

// do runs handler on a fresh context with the given request pieces.
func do(handler gin.HandlerFunc, method, target, body string, params gin.Params, headers map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	c.Params = params
	handler(c)
	return w
}

func payParams(slug, unit string) gin.Params {
	return gin.Params{{Key: "propertySlug", Value: slug}, {Key: "unitCode", Value: unit}}
}

// bearer signs a pay session for the test tenant and unit.
func (p *payTestApp) bearer(t *testing.T, unit uuid.UUID, phone string, valid time.Duration) map[string]string {
	t.Helper()
	tok, err := p.app.signPaySession(paySession{TenantID: testTenantID, UnitID: unit, Phone: phone}, time.Now().Add(valid))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + tok}
}

func (p *payTestApp) expectTarget() {
	p.store.EXPECT().ResolvePayTarget(gomock.Any(), sqlc.ResolvePayTargetParams{Slug: "runda", UnitCode: "A1"}).
		Return(sqlc.ResolvePayTargetRow{TenantID: testTenantID, PropertyID: testPropertyID, UnitID: testUnitA}, nil).AnyTimes()
}

func webhookBody(receipt, amount, ref string) string {
	return `{"response":{"Amount":` + amount + `,"ExternalReference":"` + ref + `","MpesaReceiptNumber":"` + receipt +
		`","Phone":"254722000001","ResultCode":0,"Status":"Success"}}`
}

var _ = http.StatusOK
