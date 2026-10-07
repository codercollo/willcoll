package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformmock "github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// The entitlement gates always enforce (build-phases.txt 3i-b): GATE_MODE's
// shadow mode, and the old-vs-new parity check it ran, are gone. These
// tests pin the business rule for every scenario the parity test used to
// document, plus the fail-closed behavior.

// runGateMiddleware exercises mw in isolation: manager is placed on the
// context (as requireActivatedManager already would have), then mw runs,
// then a 200 handler. Returns the recorded response.
func runGateMiddleware(mw gin.HandlerFunc, manager *platformdata.Manager) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		contextSetManager(c, manager)
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// newGateApp wires an application whose only working piece is the platform
// Modules store, mocked per test.
func newGateApp(t *testing.T, setup func(*platformmock.MockQuerier)) *application {
	t.Helper()
	ctrl := gomock.NewController(t)
	pStore := platformmock.NewMockStore(ctrl)
	pq := platformmock.NewMockQuerier(ctrl)
	pStore.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(platformsqlc.Querier) error) error { return fn(pq) }).
		AnyTimes()
	setup(pq)

	app := &application{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		platform: &platformdata.Models{Modules: platformdata.ModuleModel{Store: pStore}},
	}
	app.config.subscriptionGate = true
	return app
}

func gateManager() *platformdata.Manager {
	return &platformdata.Manager{ID: uuid.New(), Status: platformdata.ManagerStatusActive}
}

// A missing manager_modules row is a 403 (no entitlement), a platform read
// error is a 503 (the check itself failed — never a silent allow, never
// disguised as the manager's fault), a suspended module is a 403.
func TestRequireModule(t *testing.T) {
	manager := gateManager()

	tests := []struct {
		name string
		row  platformsqlc.ManagerModule
		err  error
		want int
	}{
		{"missing row", platformsqlc.ManagerModule{}, sql.ErrNoRows, http.StatusForbidden},
		{"platform read error", platformsqlc.ManagerModule{}, errors.New("connection reset"), http.StatusServiceUnavailable},
		{"suspended module", platformsqlc.ManagerModule{Status: platformdata.ModuleStatusSuspended}, nil, http.StatusForbidden},
		{"requested but not yet granted", platformsqlc.ManagerModule{Status: platformdata.ModuleStatusRequested}, nil, http.StatusForbidden},
		{"active module", platformsqlc.ManagerModule{Status: platformdata.ModuleStatusActive}, nil, http.StatusOK},
		{"trialing module", platformsqlc.ManagerModule{Status: platformdata.ModuleStatusTrialing}, nil, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := newGateApp(t, func(q *platformmock.MockQuerier) {
				q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).Return(tc.row, tc.err)
			})
			if w := runGateMiddleware(app.requireModule("rentals"), manager); w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}

	t.Run("no platform connection fails closed", func(t *testing.T) {
		app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		if w := runGateMiddleware(app.requireModule("rentals"), manager); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})
}

// A 403 from requireModule never deletes data, and re-enabling restores
// access at once (system-design.txt A.5 / E.5): the gate's only store call
// is the read of manager_modules (the mock fails on any other query, so a
// write or delete would fail the test), and the same manager passes as soon
// as the row flips back to active.
func TestRequireModule_ReEnableRestoresAccess(t *testing.T) {
	manager := gateManager()
	status := platformdata.ModuleStatusSuspended
	app := newGateApp(t, func(q *platformmock.MockQuerier) {
		q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).DoAndReturn(
			func(context.Context, platformsqlc.GetManagerModuleParams) (platformsqlc.ManagerModule, error) {
				return platformsqlc.ManagerModule{Status: status}, nil
			}).Times(3)
	})
	gate := app.requireModule("rentals")

	if w := runGateMiddleware(gate, manager); w.Code != http.StatusForbidden {
		t.Fatalf("suspended: status = %d, want 403", w.Code)
	}
	if w := runGateMiddleware(gate, manager); w.Code != http.StatusForbidden {
		t.Fatalf("suspended, second try: status = %d, want 403", w.Code)
	}
	status = platformdata.ModuleStatusActive
	if w := runGateMiddleware(gate, manager); w.Code != http.StatusOK {
		t.Fatalf("re-enabled: status = %d, want 200", w.Code)
	}
}

// TestRequirePlatformSubscription is the paywall's business rule, one row
// per scenario: trial running, trial over, a live subscription, a lapsed or
// non-live one. 402 closes the dashboard; 200 opens it.
func TestRequirePlatformSubscription(t *testing.T) {
	now := moneyfmt.Now()
	future := now.AddDate(0, 0, 3)
	past := now.AddDate(0, 0, -1)

	type subFixture struct {
		status             string
		currentPeriodStart time.Time
		currentPeriodEnd   time.Time
	}

	tests := []struct {
		name        string
		trialEndsAt *time.Time
		sub         *subFixture
		want        int
	}{
		{"trialing (no subscription, trial not yet ended)", &future, nil, http.StatusOK},
		{"trial expired (no subscription, trial ended)", &past, nil, http.StatusPaymentRequired},
		{"no trial row and no subscription", nil, nil, http.StatusPaymentRequired},
		{"active (within its period)", &past, &subFixture{platformdata.SubscriptionActive, now.AddDate(0, 0, -10), now.AddDate(0, 0, 20)}, http.StatusOK},
		{"subscription itself trialing, within period", nil, &subFixture{platformdata.SubscriptionTrialing, now.AddDate(0, 0, -5), now.AddDate(0, 0, 5)}, http.StatusOK},
		{"past_due", &past, &subFixture{platformdata.SubscriptionPastDue, now.AddDate(0, 0, -40), now.AddDate(0, 0, -10)}, http.StatusPaymentRequired},
		{"cancelled", &past, &subFixture{platformdata.SubscriptionCancelled, now.AddDate(0, 0, -40), now.AddDate(0, 0, -10)}, http.StatusPaymentRequired},
		{"active subscription past its own period end", &past, &subFixture{platformdata.SubscriptionActive, now.AddDate(0, 0, -40), now.AddDate(0, 0, -10)}, http.StatusPaymentRequired},
		{"lapsed subscription but the trial still runs", &future, &subFixture{platformdata.SubscriptionActive, now.AddDate(0, 0, -40), now.AddDate(0, 0, -10)}, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := newGateApp(t, func(q *platformmock.MockQuerier) {
				q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).
					Return(platformsqlc.ManagerModule{Status: platformdata.ModuleStatusTrialing, TrialEndsAt: tc.trialEndsAt}, nil)
				if tc.sub == nil {
					q.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).Return(platformsqlc.Subscription{}, sql.ErrNoRows)
					return
				}
				q.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).Return(platformsqlc.Subscription{
					ID: uuid.New(), PlanID: uuid.New(), Status: tc.sub.status,
					CurrentPeriodStart: tc.sub.currentPeriodStart, CurrentPeriodEnd: tc.sub.currentPeriodEnd,
				}, nil)
			})
			if w := runGateMiddleware(app.requirePlatformSubscription("rentals"), gateManager()); w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}

	t.Run("reading the module fails -> 503, never a silent allow", func(t *testing.T) {
		app := newGateApp(t, func(q *platformmock.MockQuerier) {
			q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).Return(platformsqlc.ManagerModule{}, errors.New("connection reset"))
		})
		if w := runGateMiddleware(app.requirePlatformSubscription("rentals"), gateManager()); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})

	t.Run("reading the subscription fails -> 503", func(t *testing.T) {
		app := newGateApp(t, func(q *platformmock.MockQuerier) {
			q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).Return(platformsqlc.ManagerModule{}, sql.ErrNoRows)
			q.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).Return(platformsqlc.Subscription{}, errors.New("connection reset"))
		})
		if w := runGateMiddleware(app.requirePlatformSubscription("rentals"), gateManager()); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})

	t.Run("SUBSCRIPTION_GATE off opens the gate without reading anything", func(t *testing.T) {
		app := newGateApp(t, func(q *platformmock.MockQuerier) {})
		app.config.subscriptionGate = false
		if w := runGateMiddleware(app.requirePlatformSubscription("rentals"), gateManager()); w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}
