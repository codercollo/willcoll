package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// The subscription webhook tests, moved from cmd/rentals-api when that
// binary's copy of this route was removed (build-phases.txt 3i-b): the
// PayHero billing callback now lands on platform-api only.

const testWebhookSecret = "hook-secret"

func money(t *testing.T, s string) moneyfmt.Money {
	t.Helper()
	m, err := moneyfmt.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type webhookTestApp struct {
	app    *application
	store  *mock.MockStore
	q      *mock.MockQuerier
	router *gin.Engine
}

func newWebhookTestApp(t *testing.T) *webhookTestApp {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()

	sm := scs.New()
	sm.Store = memstore.New()
	app := &application{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:         data.NewModelsFromStore(store, 0),
		sessionManager: sm,
	}
	app.config.payhero.webhookSecret = testWebhookSecret
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	return &webhookTestApp{app: app, store: store, q: q, router: router}
}

func (p *webhookTestApp) post(target, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	p.router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)))
	p.app.wg.Wait() // processing runs in the background after the 200
	return w
}

func TestSubscriptionsWebhook(t *testing.T) {
	invoice, managerID := uuid.New(), uuid.New()
	target := "/v1/webhooks/payhero/subscriptions?token=" + testWebhookSecret

	t.Run("settles the invoice and activates the subscription", func(t *testing.T) {
		p := newWebhookTestApp(t)
		subID := uuid.New()
		p.store.EXPECT().ResolveSubscriptionInvoice(gomock.Any(), invoice).Return(sqlc.ResolveSubscriptionInvoiceRow{ID: invoice, ManagerID: managerID}, nil)
		p.q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{ID: invoice, SubscriptionID: subID, Amount: money(t, "2500"), Status: "pending"}, nil)
		p.q.EXPECT().MarkSubscriptionInvoicePaid(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		p.q.EXPECT().GetSubscription(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: subID, Status: "past_due"}, nil)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionPlan{Price: money(t, "2500"), BillingInterval: "monthly"}, nil)
		p.q.EXPECT().ActivateSubscription(gomock.Any(), gomock.Any()).Return(int32(2), nil)

		body := `{"response":{"Amount":2500,"ExternalReference":"` + invoice.String() + `","MpesaReceiptNumber":"SUB1","ResultCode":0}}`
		if w := p.post(target, body); w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	})

	t.Run("activates the plan chosen on the invoice, not the subscription's current one", func(t *testing.T) {
		p := newWebhookTestApp(t)
		currentPlanID, chosenPlanID := uuid.New(), uuid.New()
		subID := uuid.New()
		p.store.EXPECT().ResolveSubscriptionInvoice(gomock.Any(), invoice).Return(sqlc.ResolveSubscriptionInvoiceRow{ID: invoice, ManagerID: managerID}, nil)
		p.q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{ID: invoice, SubscriptionID: subID, PlanID: chosenPlanID, Amount: money(t, "3500"), Status: "pending"}, nil)
		p.q.EXPECT().MarkSubscriptionInvoicePaid(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		p.q.EXPECT().GetSubscription(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: subID, PlanID: currentPlanID, Status: "active"}, nil)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), chosenPlanID).Return(sqlc.SubscriptionPlan{ID: chosenPlanID, Price: money(t, "3500"), BillingInterval: "monthly"}, nil)
		p.q.EXPECT().ActivateSubscription(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.ActivateSubscriptionParams) (int32, error) {
				if a.PlanID != chosenPlanID {
					t.Errorf("activated plan = %s, want the invoice's chosen plan %s", a.PlanID, chosenPlanID)
				}
				return 2, nil
			})

		body := `{"response":{"Amount":3500,"ExternalReference":"` + invoice.String() + `","MpesaReceiptNumber":"SUB2","ResultCode":0}}`
		if w := p.post(target, body); w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	})

	t.Run("unauthorized callbacks are refused before anything is read", func(t *testing.T) {
		p := newWebhookTestApp(t)
		if w := p.post("/v1/webhooks/payhero/subscriptions", `{}`); w.Code != http.StatusUnauthorized {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("a callback with no invoice reference is a 400", func(t *testing.T) {
		p := newWebhookTestApp(t)
		if w := p.post(target, `{"response":{"Amount":1,"MpesaReceiptNumber":"X"}}`); w.Code != http.StatusBadRequest {
			t.Errorf("status %d", w.Code)
		}
	})
}
