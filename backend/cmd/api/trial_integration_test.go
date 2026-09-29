package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestTrialAndPaywallEndToEnd walks a firm through its life: activation starts
// a 7 day trial, the dashboard is open during it, the paywall (402) closes it
// when the trial ends except for /account and /billing, and a paid subscription
// reopens it until the paid period runs out.
func TestTrialAndPaywallEndToEnd(t *testing.T) {
	appDSN, adminDSN := os.Getenv("WILLCOLL_TEST_DB_DSN"), os.Getenv("WILLCOLL_TEST_ADMIN_DB_DSN")
	if appDSN == "" || adminDSN == "" {
		t.Skip("WILLCOLL_TEST_DB_DSN and WILLCOLL_TEST_ADMIN_DB_DSN not set; skipping")
	}
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
	ctx := context.Background()

	// --- A firm that has signed up but not activated.
	email := uuid.NewString()[:8] + "@trial.example.com"
	m := &data.Manager{FirmName: "Trial Firm", Username: "u" + uuid.NewString()[:10], Email: email, Phone: "+254700000000", Status: data.ManagerStatusPending}
	if err := m.Password.Set("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := models.Managers.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Exec(`DELETE FROM managers WHERE id = $1`, m.ID) })
	if m.TrialEndsAt != nil {
		t.Fatalf("a pending firm already has a trial: %v", m.TrialEndsAt)
	}

	sm := scs.New()
	sm.Store = memstore.New()
	app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models, sessionManager: sm}
	app.config.env = "development"
	app.config.trialDays = 7
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
		if c := w.Result().Cookies(); len(c) > 0 && out != nil {
			out["_cookie"], _ = json.Marshal(c[0].Name + "=" + c[0].Value)
		}
		return w.Code, out
	}
	type access struct {
		State    string `json:"state"`
		DaysLeft int    `json:"days_left"`
	}
	accessOf := func(cookie string) access {
		code, body := call(http.MethodGet, "/v1/sessions", "", cookie)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/sessions: %d", code)
		}
		var a access
		if err := json.Unmarshal(body["access"], &a); err != nil {
			t.Fatalf("no access in the session response: %v (%s)", err, body["access"])
		}
		return a
	}

	// --- Activation starts the trial (once).
	token, err := models.Tokens.New(ctx, m.ID, time.Hour, data.ScopeActivation)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := call(http.MethodPut, "/v1/managers/activated", `{"token":"`+token.Plaintext+`"}`, ""); code != http.StatusOK {
		t.Fatalf("activate: %d %s", code, body["error"])
	}
	var trialEnds time.Time
	if err := conn.QueryRow(`SELECT trial_ends_at FROM managers WHERE id = $1`, m.ID).Scan(&trialEnds); err != nil {
		t.Fatalf("no trial recorded at activation: %v", err)
	}
	if d := time.Until(trialEnds); d < 6*24*time.Hour+23*time.Hour || d > 7*24*time.Hour+time.Minute {
		t.Errorf("trial ends in %v, want about 7 days", d)
	}

	// --- Sign in: the dashboard is open and the countdown is reported.
	code, body := call(http.MethodPost, "/v1/sessions", `{"email":"`+email+`","password":"correct-horse-battery"}`, "")
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body["error"])
	}
	var cookie string
	_ = json.Unmarshal(body["_cookie"], &cookie)
	if a := accessOf(cookie); a.State != data.AccessTrial || a.DaysLeft != 7 {
		t.Errorf("first day: %+v, want trial with 7 days", a)
	}
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Errorf("GET /v1/properties during the trial: %d, want 200", code)
	}

	// --- The trial ends: the paywall closes the dashboard but not the way to pay.
	if _, err := conn.Exec(`UPDATE managers SET trial_ends_at = now() - interval '1 minute' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/properties", "/v1/landlords", "/v1/payments/review"} {
		code, body := call(http.MethodGet, path, "", cookie)
		if code != http.StatusPaymentRequired || !strings.Contains(string(body["error"]), "choose a plan") {
			t.Errorf("GET %s after the trial: %d %s, want 402 with the paywall message", path, code, body["error"])
		}
	}
	if code, _ := call(http.MethodPost, "/v1/landlords", `{"name":"X","phone":"+254711111111"}`, cookie); code != http.StatusPaymentRequired {
		t.Errorf("writes are closed too: POST /v1/landlords = %d, want 402", code)
	}
	for _, path := range []string{"/v1/account", "/v1/billing/plans", "/v1/billing/invoices", "/v1/sessions"} {
		if code, _ := call(http.MethodGet, path, "", cookie); code != http.StatusOK {
			t.Errorf("GET %s after the trial: %d, want 200 (the way to pay stays open)", path, code)
		}
	}
	if a := accessOf(cookie); a.State != data.AccessExpired || a.DaysLeft != 0 {
		t.Errorf("after the trial: %+v, want expired", a)
	}
	if code, _ := call(http.MethodGet, "/v1/billing/subscription", "", cookie); code == http.StatusPaymentRequired {
		t.Error("/v1/billing/subscription must not be paywalled")
	}

	// --- Buying a plan reopens it.
	plan, err := models.Platform.CreatePlan(ctx, data.PlanInput{Name: "Trial Test " + uuid.NewString()[:6], Price: money("3500"), BillingInterval: "monthly"}, false)
	if err != nil {
		t.Fatal(err)
	}
	tenantSQL(t, conn, m.ID, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO subscriptions (manager_id, plan_id, status, current_period_start, current_period_end)
			VALUES ($1, $2, 'active', current_date, current_date + 30)`, m.ID, plan.ID); err != nil {
			t.Fatal(err)
		}
	})
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusOK {
		t.Errorf("GET /v1/properties with a paid plan: %d, want 200", code)
	}
	if a := accessOf(cookie); a.State != data.AccessSubscribed || a.DaysLeft < 30 {
		t.Errorf("subscribed: %+v", a)
	}

	// --- ...until the paid period runs out.
	tenantSQL(t, conn, m.ID, func(tx *sql.Tx) {
		if _, err := tx.Exec(`UPDATE subscriptions SET current_period_end = current_date - 1 WHERE manager_id = $1`, m.ID); err != nil {
			t.Fatal(err)
		}
	})
	if code, _ := call(http.MethodGet, "/v1/properties", "", cookie); code != http.StatusPaymentRequired {
		t.Errorf("GET /v1/properties after the paid period ended: %d, want 402", code)
	}

	// --- Activating again never restarts a trial.
	if _, err := conn.Exec(`UPDATE managers SET trial_ends_at = now() - interval '1 day' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	token2, _ := models.Tokens.New(ctx, m.ID, time.Hour, data.ScopeActivation)
	call(http.MethodPut, "/v1/managers/activated", `{"token":"`+token2.Plaintext+`"}`, "")
	var again time.Time
	_ = conn.QueryRow(`SELECT trial_ends_at FROM managers WHERE id = $1`, m.ID).Scan(&again)
	if !again.Before(time.Now()) {
		t.Errorf("re-activation restarted the trial: %v", again)
	}
}
