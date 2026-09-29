package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode) // no route-table debug output
	os.Exit(m.Run())
}

var (
	testManagerID = uuid.MustParse("00000000-0000-0000-0000-0000000000c1")
	testAdminID   = uuid.MustParse("00000000-0000-0000-0000-0000000000d1")
)

type middlewareTestApp struct {
	app    *application
	store  *mock.MockStore
	router *gin.Engine
}

func newMiddlewareTestApp(t *testing.T) *middlewareTestApp {
	t.Helper()

	store := mock.NewMockStore(gomock.NewController(t))

	sm := scs.New()
	sm.Store = memstore.New()
	sm.Cookie.Name = "willcoll_session"

	app := &application{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:         data.NewModelsFromStore(store, 0),
		sessionManager: sm,
	}
	app.config.env = "development"
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}

	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	return &middlewareTestApp{app: app, store: store, router: router}
}

// sessionCookie commits a session holding the given values and returns its
// cookie, as if a previous request had logged in.
func (m *middlewareTestApp) sessionCookie(t *testing.T, values map[string]string) *http.Cookie {
	t.Helper()
	sm := m.app.sessionManager

	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range values {
		sm.Put(ctx, k, v)
	}
	token, _, err := sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sm.Cookie.Name, Value: token}
}

func (m *middlewareTestApp) do(method, target string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	m.router.ServeHTTP(w, req)
	return w
}

func managerRow(status string) sqlc.Manager {
	return sqlc.Manager{
		ID: testManagerID, FirmName: "Firm", Username: "firm", Email: "firm@example.com",
		Phone: "+254700000000", PasswordHash: []byte("x"), Status: status, Version: 1,
	}
}

func TestRequireActivatedManager(t *testing.T) {
	managerSession := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}

	// GET /v1/properties/not-a-uuid answers 404 from the handler itself, so
	// a 404 proves the request got past the guard without touching the DB.
	const target = "/v1/properties/not-a-uuid"

	tests := []struct {
		name       string
		status     string // manager status; "" = anonymous
		wantStatus int
		wantBody   string
	}{
		{"anonymous", "", http.StatusUnauthorized, "must be authenticated"},
		{"pending", data.ManagerStatusPending, http.StatusForbidden, "must be activated"},
		{"suspended", data.ManagerStatusSuspended, http.StatusForbidden, "has been suspended"},
		{"active", data.ManagerStatusActive, http.StatusNotFound, "could not be found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)

			var cookie *http.Cookie
			if tt.status != "" {
				m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(tt.status), nil)
				cookie = m.sessionCookie(t, managerSession)
			}

			w := m.do(http.MethodGet, target, cookie, nil)
			if w.Code != tt.wantStatus || !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("status = %d body = %s; want %d containing %q", w.Code, w.Body, tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	adminSession := map[string]string{sessionKeyPrincipalType: principalAdmin, sessionKeyPrincipalID: testAdminID.String()}
	managerSession := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}

	t.Run("admin reaches admin routes", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetAdmin(gomock.Any(), testAdminID).Return(sqlc.Admin{ID: testAdminID, Name: "Root"}, nil)

		m.store.EXPECT().AdminListManagers(gomock.Any(), gomock.Any()).Return(nil, nil)

		if w := m.do(http.MethodGet, "/v1/admin/managers", m.sessionCookie(t, adminSession), nil); w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("admin is not a manager", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetAdmin(gomock.Any(), testAdminID).Return(sqlc.Admin{ID: testAdminID}, nil)

		if w := m.do(http.MethodGet, "/v1/properties", m.sessionCookie(t, adminSession), nil); w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("manager is not an admin", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(data.ManagerStatusActive), nil)

		if w := m.do(http.MethodGet, "/v1/admin/managers", m.sessionCookie(t, managerSession), nil); w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		if w := m.do(http.MethodGet, "/v1/admin/managers", nil, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})
}

func TestAuthenticateStaleSession(t *testing.T) {
	m := newMiddlewareTestApp(t)
	cookie := m.sessionCookie(t, map[string]string{
		sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String(),
	})

	// The manager behind the session no longer exists.
	m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(sqlc.Manager{}, sql.ErrNoRows)

	if w := m.do(http.MethodGet, "/v1/properties", cookie, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}

	// The dead principal was removed from the session, so the next request
	// doesn't look the manager up again (the mock would fail if it did).
	if w := m.do(http.MethodGet, "/v1/properties", cookie, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("second request status = %d, want 401", w.Code)
	}
}

// sessionTestRouter mounts helper-exercising routes behind the session
// middleware only.
func sessionTestRouter(app *application) *gin.Engine {
	r := gin.New()
	r.Use(app.loadAndSaveSession())
	r.POST("/flash", func(c *gin.Context) {
		app.putFlash(c, "Your account has been activated")
		app.writeJSON(c, http.StatusOK, envelope{"ok": true}, nil)
	})
	r.GET("/page", func(c *gin.Context) {
		app.writeJSON(c, http.StatusOK, envelope{"ok": true}, nil)
	})
	r.POST("/login", func(c *gin.Context) {
		if err := app.loginPrincipal(c, principalManager, testManagerID); err != nil {
			app.serverErrorResponse(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	r.POST("/logout", func(c *gin.Context) {
		if err := app.logoutPrincipal(c); err != nil {
			app.serverErrorResponse(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	return r
}

func sessionCookieFrom(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "willcoll_session" {
			return ck
		}
	}
	t.Fatalf("no session cookie in response (headers: %v)", w.Header())
	return nil
}

func TestFlashMessages(t *testing.T) {
	m := newMiddlewareTestApp(t)
	r := sessionTestRouter(m.app)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/flash", nil))
	cookie := sessionCookieFrom(t, w)

	get := func() string {
		req := httptest.NewRequest(http.MethodGet, "/page", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Body.String()
	}

	if body := get(); !strings.Contains(body, `"flash": "Your account has been activated"`) {
		t.Errorf("first response should carry the flash, got %s", body)
	}
	if body := get(); strings.Contains(body, "flash") {
		t.Errorf("flash must be delivered only once, got %s", body)
	}
}

func TestLoginRenewsTokenAndLogoutDestroys(t *testing.T) {
	m := newMiddlewareTestApp(t)
	r := sessionTestRouter(m.app)

	before := m.sessionCookie(t, map[string]string{"planted": "by attacker"})

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.AddCookie(before)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	after := sessionCookieFrom(t, w)

	if w.Code != http.StatusNoContent || after.Value == before.Value || after.Value == "" {
		t.Fatalf("login: status %d, token %q -> %q; want a new token", w.Code, before.Value, after.Value)
	}

	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(after)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if cleared := sessionCookieFrom(t, w); cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("logout should clear the cookie, got %+v", cleared)
	}
}

func TestEnableCORS(t *testing.T) {
	m := newMiddlewareTestApp(t)
	preflight := map[string]string{"Origin": "http://localhost:3000", "Access-Control-Request-Method": "PATCH"}

	w := m.do(http.MethodOptions, "/v1/properties", nil, preflight)
	if w.Code != http.StatusOK ||
		w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		w.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Errorf("trusted preflight: status %d headers %v", w.Code, w.Header())
	}

	preflight["Origin"] = "https://evil.example"
	if w := m.do(http.MethodOptions, "/v1/properties", nil, preflight); w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("untrusted origin got CORS headers: %v", w.Header())
	}
}

func TestCORSPreflightForThePayPage(t *testing.T) {
	m := newMiddlewareTestApp(t)
	preflight := map[string]string{
		"Origin":                         "http://localhost:3000",
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization,content-type",
	}
	w := m.do(http.MethodOptions, "/v1/pay/runda/A1/intent", nil, preflight)
	allowed := w.Header().Get("Access-Control-Allow-Headers")
	if w.Code != http.StatusOK || !strings.Contains(allowed, "Authorization") || !strings.Contains(allowed, "Content-Type") {
		t.Errorf("pay preflight: status %d, allowed headers %q; the Bearer token header must be allowed", w.Code, allowed)
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("methods %q", w.Header().Get("Access-Control-Allow-Methods"))
	}
}

func TestMetricsEndpoint(t *testing.T) {
	m := newMiddlewareTestApp(t)
	m.do(http.MethodGet, "/v1/healthcheck", nil, nil)

	w := m.do(http.MethodGet, "/debug/vars", nil, nil)
	for _, key := range []string{"requests_received_total", "responses_sent_total", "processing_time_microseconds", "responses_sent_by_status"} {
		if !strings.Contains(w.Body.String(), key) {
			t.Errorf("/debug/vars missing %s", key)
		}
	}
}

func TestRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	m := newMiddlewareTestApp(t)
	m.app.config.limiter.enabled = true
	m.app.config.limiter.rps = 0.001
	m.app.config.limiter.burst = 1
	router, err := m.app.routes() // rebuild with the limiter on
	if err != nil {
		t.Fatal(err)
	}
	m.router = router

	// Same TCP peer, different X-Forwarded-For each time. No proxy is
	// trusted, so the header is ignored and the second request is limited.
	first := m.do(http.MethodGet, "/v1/pay/some-property/1A", nil, map[string]string{"X-Forwarded-For": "203.0.113.1"})
	second := m.do(http.MethodGet, "/v1/pay/some-property/1A", nil, map[string]string{"X-Forwarded-For": "203.0.113.2"})

	if first.Code == http.StatusTooManyRequests || second.Code != http.StatusTooManyRequests {
		t.Errorf("statuses = %d, %d; want the second limited", first.Code, second.Code)
	}

	// Webhooks are exempt.
	for i := 0; i < 3; i++ {
		if w := m.do(http.MethodPost, "/v1/webhooks/payhero/collections", nil, nil); w.Code == http.StatusTooManyRequests {
			t.Fatal("webhooks must not be rate limited")
		}
	}
}

func TestRedactQueryToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"webhook secret redacted", "/v1/webhooks/payhero/collections?token=super-secret-123", "/v1/webhooks/payhero/collections?token=REDACTED"},
		{"other params untouched, order-independent", "/v1/webhooks/payhero/collections?token=abc&source=mpesa", ""}, // checked below
		{"no token param, unchanged", "/v1/healthcheck", "/v1/healthcheck"},
		{"empty token still redacted", "/v1/webhooks/payhero/collections?token=", "/v1/webhooks/payhero/collections?token=REDACTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			got := redactQueryToken(u)
			if strings.Contains(got, "super-secret-123") || strings.Contains(got, "abc") {
				t.Errorf("redactQueryToken(%q) = %q, leaked the token value", tc.in, got)
			}
			if !strings.Contains(got, "token=REDACTED") && strings.Contains(tc.in, "token=") {
				t.Errorf("redactQueryToken(%q) = %q, want token=REDACTED", tc.in, got)
			}
			if tc.want != "" && got != tc.want {
				t.Errorf("redactQueryToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
