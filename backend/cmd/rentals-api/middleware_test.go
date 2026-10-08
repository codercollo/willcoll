package main

import (
	"context"
	"database/sql"
	"errors"
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
	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformmock "github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/mock"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
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
	pStore *platformmock.MockStore
	pq     *platformmock.MockQuerier
	router *gin.Engine
}

// newMiddlewareTestApp wires the app the way main() does after build-phases.txt
// 3i-b: every principal comes from a willcoll_platform session (a memstore
// here, standing in for the shared sessions table) and a mocked platform
// store; the rentals mock store is for the handlers behind the guards.
func newMiddlewareTestApp(t *testing.T) *middlewareTestApp {
	t.Helper()

	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)

	pStore := platformmock.NewMockStore(ctrl)
	pq := platformmock.NewMockQuerier(ctrl)
	pStore.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(platformsqlc.Querier) error) error { return fn(pq) }).
		AnyTimes()
	// requireModule("rentals") runs on every manager route; keep it a
	// harmless active module here, since these tests are about who the
	// caller is and what they may do, not entitlement (gates_test.go).
	pq.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).
		Return(platformsqlc.ManagerModule{Status: platformdata.ModuleStatusActive}, nil).AnyTimes()
	platformModels := platformdata.NewModelsFromStore(pStore, 0)

	psm := scs.New()
	psm.Store = memstore.New()
	psm.Cookie.Name = platformSessionCookieName

	app := &application{
		logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:                 data.NewModelsFromStore(store, 0),
		platform:               &platformModels,
		platformSessionManager: psm,
	}
	app.config.env = "development"
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}

	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	return &middlewareTestApp{app: app, store: store, pStore: pStore, pq: pq, router: router}
}

// sessionCookie commits a session in the platform store holding the given
// values and returns its cookie, as if platform-api had signed someone in.
// rentals-api itself never writes one.
func (m *middlewareTestApp) sessionCookie(t *testing.T, values map[string]string) *http.Cookie {
	t.Helper()
	sm := m.app.platformSessionManager

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

func managerRow(status string) platformsqlc.Manager {
	return platformsqlc.Manager{
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
		{"pending", platformdata.ManagerStatusPending, http.StatusForbidden, "must be activated"},
		{"suspended", platformdata.ManagerStatusSuspended, http.StatusForbidden, "has been suspended"},
		{"active", platformdata.ManagerStatusActive, http.StatusNotFound, "could not be found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)

			var cookie *http.Cookie
			if tt.status != "" {
				m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(tt.status), nil)
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
		m.pStore.EXPECT().GetAdmin(gomock.Any(), testAdminID).Return(platformsqlc.Admin{ID: testAdminID, Name: "Root"}, nil)

		// /v1/admin/backups is rentals-api's own Super Admin route left after
		// manager oversight moved to platform-api (3i-b).
		m.store.EXPECT().DatabaseHealth(gomock.Any()).Return(db.DatabaseHealth{}, nil)
		m.store.EXPECT().GetSystemMetadata(gomock.Any(), gomock.Any()).Return(sqlc.SystemMetadatum{}, sql.ErrNoRows)

		if w := m.do(http.MethodGet, "/v1/admin/backups", m.sessionCookie(t, adminSession), nil); w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("admin is not a manager", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.pStore.EXPECT().GetAdmin(gomock.Any(), testAdminID).Return(platformsqlc.Admin{ID: testAdminID}, nil)

		if w := m.do(http.MethodGet, "/v1/properties", m.sessionCookie(t, adminSession), nil); w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("manager is not an admin", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil)

		if w := m.do(http.MethodGet, "/v1/admin/backups", m.sessionCookie(t, managerSession), nil); w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		if w := m.do(http.MethodGet, "/v1/admin/backups", nil, nil); w.Code != http.StatusUnauthorized {
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
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(platformsqlc.Manager{}, sql.ErrNoRows).Times(2)

	if w := m.do(http.MethodGet, "/v1/properties", cookie, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}

	// rentals-api only reads the platform session (never edits it, so it
	// can't forget the dead principal): the next request resolves it again
	// and is just as anonymous.
	if w := m.do(http.MethodGet, "/v1/properties", cookie, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("second request status = %d, want 401", w.Code)
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

// /debug/vars is served only by the internal metrics listener; the public
// router must not have it at all (authorization.md M13).
func TestDebugVarsNotOnPublicRouter(t *testing.T) {
	m := newMiddlewareTestApp(t)
	for _, target := range []string{"/debug/vars", "/debug/pprof/", "/debug/"} {
		if w := m.do(http.MethodGet, target, nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s through the public router: status %d, want 404", target, w.Code)
		}
	}
}

func TestMetricsListenerServesDebugVarsOnly(t *testing.T) {
	m := newMiddlewareTestApp(t)
	m.do(http.MethodGet, "/v1/healthcheck", nil, nil)

	h := httpx.NewMetricsServer("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))
	for _, key := range []string{"requests_received_total", "responses_sent_total", "processing_time_microseconds", "responses_sent_by_status"} {
		if !strings.Contains(w.Body.String(), key) {
			t.Errorf("/debug/vars missing %s", key)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/healthcheck", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("metrics listener served /v1/healthcheck: status %d, want 404", w.Code)
	}
}

func TestRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	m := newMiddlewareTestApp(t)
	m.app.config.limiter.enabled = true
	m.app.config.limiter.rps = 0.001
	m.app.config.limiter.burst = 1
	m.app.config.pay.ipRPS = 0.001 // the pay route has its own (normally loose) per-IP limiter
	m.app.config.pay.ipBurst = 1
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
			got := httpx.RedactQueryToken(u)
			if strings.Contains(got, "super-secret-123") || strings.Contains(got, "abc") {
				t.Errorf("httpx.RedactQueryToken(%q) = %q, leaked the token value", tc.in, got)
			}
			if !strings.Contains(got, "token=REDACTED") && strings.Contains(tc.in, "token=") {
				t.Errorf("httpx.RedactQueryToken(%q) = %q, want token=REDACTED", tc.in, got)
			}
			if tc.want != "" && got != tc.want {
				t.Errorf("httpx.RedactQueryToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSuspendedFirmIsRefused is rentals-api's half of build-phases.txt
// 3i-b's suspend/reinstate rule (platform-api's half is
// cmd/platform-api's admin_managers_test.go): platform-api's suspend
// destroys every session of the manager AND their staff in the shared
// sessions table and flips managers.status; this binary must then refuse
// both principals on their next request — whether the session row is gone
// (401) or somehow still loadable (403, because status is read from
// willcoll_platform on every request, never cached in the session) — and
// accept them again after a reinstatement and a fresh sign-in.
func TestSuspendedFirmIsRefused(t *testing.T) {
	const target = "/v1/properties/not-a-uuid" // 404 from the handler = past every guard
	staff := testStaffRow(platformdata.RoleOwner, "rentals")
	managerSession := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}
	staffSession := map[string]string{sessionKeyPrincipalType: principalStaff, sessionKeyPrincipalID: staff.ID.String()}

	// destroy plays platform-api's revocation: delete the session row.
	destroy := func(t *testing.T, m *middlewareTestApp, cookie *http.Cookie) {
		t.Helper()
		sm := m.app.platformSessionManager
		ctx, err := sm.Load(context.Background(), cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		if err := sm.Destroy(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := sm.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	for _, who := range []struct {
		name    string
		session map[string]string
	}{{"manager", managerSession}, {"staff", staffSession}} {
		t.Run(who.name+": session still loadable but firm suspended -> 403", func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
			m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusSuspended), nil)
			w := m.do(http.MethodGet, target, m.sessionCookie(t, who.session), nil)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "suspended") {
				t.Errorf("status = %d body = %s; want 403 suspended", w.Code, w.Body)
			}
		})

		t.Run(who.name+": session revoked -> 401", func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			cookie := m.sessionCookie(t, who.session)
			destroy(t, m, cookie)
			// No GetManager/GetStaff expectation: a revoked session must
			// not even reach a lookup.
			if w := m.do(http.MethodGet, target, cookie, nil); w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})

		t.Run(who.name+": reinstated firm signs in again -> past every guard", func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
			m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil)
			if w := m.do(http.MethodGet, target, m.sessionCookie(t, who.session), nil); w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (past the guards)", w.Code)
			}
		})
	}

	t.Run("a platform read failure is a 503, never an anonymous pass-through", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(platformsqlc.Manager{}, errors.New("connection reset"))
		if w := m.do(http.MethodGet, target, m.sessionCookie(t, managerSession), nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})

	t.Run("a session cookie with no platform connection stays anonymous", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		cookie := m.sessionCookie(t, managerSession)
		m.app.platform = nil
		if w := m.do(http.MethodGet, target, cookie, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})
}
