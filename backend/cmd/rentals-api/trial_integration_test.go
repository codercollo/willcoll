package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestTrialAndPaywallEndToEnd walks a firm through its life against rentals-api
// (build-phases.txt 3i-b: rewritten to sign in through platform-api's session
// instead of rentals-api's retired local login): the dashboard is open during
// the trial, the paywall (402) closes it when the trial ends, a paid
// subscription (a renewal, as platform-api records it) reopens it instantly,
// it closes again when the paid period runs out, and a suspension from the
// Super Admin shuts the firm out whatever else is true.
//
// The real Rentals database sits behind the handlers; willcoll_platform is
// the in-memory platformStub, and the session it holds is exactly the row
// platform-api's login writes (a principal_type/principal_id pair in the
// shared sessions store) — rentals-api reads it and never mints one.
func TestTrialAndPaywallEndToEnd(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_DB_DSN", "WILLCOLL_TEST_ADMIN_DB_DSN")
	appDSN, adminDSN := dsns[0], dsns[1]
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err := db.Ping(); err != nil {
			t.Fatal(err)
		}
		return db
	}
	conn, adminConn := open(appDSN), open(adminDSN)
	models := data.NewModels(conn, adminConn, 5*time.Second)
	_ = context.Background()

	// --- A firm that has activated: platform-api started its Rentals trial.
	stub := newPlatformStub(t)
	tenant := stub.newFirm()
	trialEnds := moneyfmt.Now().Add(7 * 24 * time.Hour)
	stub.edit(tenant, func(f *stubFirm) {
		f.ModuleState = platformdata.ModuleStatusTrialing
		f.TrialEndsAt = &trialEnds
	})

	app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models}
	stub.attach(app)
	app.config.env = "development"
	app.config.subscriptionGate = true
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, cookie string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	paywalled := func(cookie string) {
		t.Helper()
		for _, path := range []string{"/v1/properties", "/v1/landlords", "/v1/payments/review"} {
			code, body := call(http.MethodGet, path, "", cookie)
			if code != http.StatusPaymentRequired || !strings.Contains(string(body["error"]), "choose a plan") {
				t.Errorf("GET %s: %d %s, want 402 with the paywall message", path, code, body["error"])
			}
		}
		if code, _ := call(http.MethodPost, "/v1/landlords", `{"name":"X","phone":"+254711111111"}`, cookie); code != http.StatusPaymentRequired {
			t.Errorf("writes are closed too: POST /v1/landlords = %d, want 402", code)
		}
	}

	// --- Signed out is still signed out.
	if code, _ := call(http.MethodGet, "/v1/properties", "", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", code)
	}

	// --- Signed in through platform-api: the dashboard is open during the trial.
	cookie := stub.loginManager(t, tenant)
	if code, body := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Fatalf("GET /v1/properties during the trial: %d %s, want 200", code, body["error"])
	}

	// --- The trial ends: the paywall closes the dashboard...
	past := moneyfmt.Now().Add(-time.Minute)
	stub.edit(tenant, func(f *stubFirm) { f.TrialEndsAt = &past })
	paywalled(cookie)
	// ...but not the tenant-facing pay page: a lapsed firm's tenants can
	// still pay their rent (the route 404s for this made-up property, it
	// is never paywalled).
	if code, _ := call(http.MethodGet, "/v1/pay/no-such-property/A1", "", ""); code == http.StatusPaymentRequired {
		t.Errorf("the public pay page must never be paywalled, got %d", code)
	}

	// --- A paid renewal (platform-api's webhook activated a subscription)
	// reopens it at once, with no new login.
	stub.edit(tenant, func(f *stubFirm) {
		now := moneyfmt.Now()
		f.ModuleState = platformdata.ModuleStatusActive
		f.Subscription = &platformsqlc.Subscription{
			ID: uuid.New(), ManagerID: tenant, Product: platformdata.ProductRentals, PlanID: uuid.New(),
			Status: platformdata.SubscriptionActive, CurrentPeriodStart: now, CurrentPeriodEnd: now.AddDate(0, 0, 30),
		}
	})
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Errorf("GET /v1/properties with a paid plan: %d, want 200", code)
	}

	// --- ...until the paid period runs out.
	stub.edit(tenant, func(f *stubFirm) { f.Subscription.CurrentPeriodEnd = moneyfmt.Now().AddDate(0, 0, -2) })
	paywalled(cookie)

	// --- Renewing again reopens it.
	stub.edit(tenant, func(f *stubFirm) { f.Subscription.CurrentPeriodEnd = moneyfmt.Now().AddDate(0, 0, 30) })
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Errorf("GET /v1/properties after renewing: %d, want 200", code)
	}

	// --- A suspended Rentals module is a 403, not a paywall.
	stub.edit(tenant, func(f *stubFirm) { f.ModuleState = platformdata.ModuleStatusSuspended })
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusForbidden {
		t.Errorf("suspended module: %d, want 403", code)
	}
	stub.edit(tenant, func(f *stubFirm) { f.ModuleState = platformdata.ModuleStatusActive })
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Errorf("module restored: %d, want 200 (data intact)", code)
	}

	// --- A suspended firm is refused on its very next request.
	stub.edit(tenant, func(f *stubFirm) { f.Status = platformdata.ManagerStatusSuspended })
	if code, body := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusForbidden || !strings.Contains(string(body["error"]), "suspended") {
		t.Errorf("suspended firm: %d %s, want 403 suspended", code, body["error"])
	}
	stub.edit(tenant, func(f *stubFirm) { f.Status = platformdata.ManagerStatusActive })
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Errorf("reinstated firm: %d, want 200", code)
	}
}
