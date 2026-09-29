package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "correct horse battery"

func testHash(t *testing.T) []byte {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// send makes a request with an optional body through the full router.
func (m *middlewareTestApp) send(method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	m.router.ServeHTTP(w, req)
	return w
}

func cookieFrom(w *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "willcoll_session" {
			return ck
		}
	}
	return nil
}

func loginBody(email, password string) string {
	return `{"email":"` + email + `","password":"` + password + `"}`
}

func TestManagerLoginLogout(t *testing.T) {
	m := newMiddlewareTestApp(t)

	row := managerRow(data.ManagerStatusActive)
	row.PasswordHash = testHash(t)
	m.store.EXPECT().GetManagerByEmail(gomock.Any(), "firm@example.com").Return(row, nil)
	m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(row, nil).AnyTimes()

	w := m.send(http.MethodPost, "/v1/sessions", loginBody("firm@example.com", testPassword), nil)
	cookie := cookieFrom(w)
	if w.Code != http.StatusOK || cookie == nil || cookie.Value == "" || !cookie.HttpOnly {
		t.Fatalf("login: status %d, cookie %+v, body %s", w.Code, cookie, w.Body)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Error("login response leaked a password field")
	}

	// Who am I?
	if w := m.do(http.MethodGet, "/v1/sessions", cookie, nil); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"principal_type": "manager"`) {
		t.Errorf("GET /v1/sessions: status %d body %s", w.Code, w.Body)
	}

	// The session opens the manager dashboard (404 = past the guard).
	if w := m.do(http.MethodGet, "/v1/properties/not-a-uuid", cookie, nil); w.Code != http.StatusNotFound {
		t.Errorf("dashboard route with session: status %d", w.Code)
	}

	// Logout destroys it.
	w = m.send(http.MethodDelete, "/v1/sessions", "", cookie)
	if cleared := cookieFrom(w); w.Code != http.StatusOK || cleared == nil || cleared.Value != "" {
		t.Errorf("logout: status %d, cookie %+v", w.Code, cleared)
	}
	if w := m.do(http.MethodGet, "/v1/sessions", cookie, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("old cookie after logout: status %d, want 401", w.Code)
	}
}

func TestManagerLoginFailures(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		setup      func(m *middlewareTestApp)
		wantStatus int
		wantBody   string
	}{
		{
			name: "wrong password",
			body: loginBody("firm@example.com", "not the password"),
			setup: func(m *middlewareTestApp) {
				row := managerRow(data.ManagerStatusActive)
				row.PasswordHash = testHash(t)
				m.store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(row, nil)
			},
			wantStatus: http.StatusUnauthorized, wantBody: "invalid authentication credentials",
		},
		{
			name: "unknown email looks the same",
			body: loginBody("nobody@example.com", "whatever it is"),
			setup: func(m *middlewareTestApp) {
				m.store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(sqlc.Manager{}, sql.ErrNoRows)
			},
			wantStatus: http.StatusUnauthorized, wantBody: "invalid authentication credentials",
		},
		{
			name: "suspended",
			body: loginBody("firm@example.com", testPassword),
			setup: func(m *middlewareTestApp) {
				row := managerRow(data.ManagerStatusSuspended)
				row.PasswordHash = testHash(t)
				m.store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(row, nil)
			},
			wantStatus: http.StatusForbidden, wantBody: "has been suspended",
		},
		{
			name:       "malformed",
			body:       loginBody("not-an-email", ""),
			setup:      func(_ *middlewareTestApp) {},
			wantStatus: http.StatusUnprocessableEntity, wantBody: `"email"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			tt.setup(m)

			w := m.send(http.MethodPost, "/v1/sessions", tt.body, nil)
			if w.Code != tt.wantStatus || !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("status %d body %s; want %d containing %q", w.Code, w.Body, tt.wantStatus, tt.wantBody)
			}
			if ck := cookieFrom(w); ck != nil && ck.Value != "" {
				t.Error("a failed login must not issue a session")
			}
		})
	}
}

func TestAdminLoginAndSuspend(t *testing.T) {
	m := newMiddlewareTestApp(t)

	adminRow := sqlc.Admin{ID: testAdminID, Name: "Root", Email: "root@willcoll.app", PasswordHash: testHash(t)}
	m.store.EXPECT().GetAdminByEmail(gomock.Any(), "root@willcoll.app").Return(adminRow, nil)
	m.store.EXPECT().GetAdmin(gomock.Any(), testAdminID).Return(adminRow, nil).AnyTimes()

	w := m.send(http.MethodPost, "/v1/admin/sessions", loginBody("root@willcoll.app", testPassword), nil)
	adminCookie := cookieFrom(w)
	if w.Code != http.StatusOK || adminCookie == nil || !strings.Contains(w.Body.String(), `"admin"`) {
		t.Fatalf("admin login: status %d body %s", w.Code, w.Body)
	}
	if w := m.do(http.MethodGet, "/v1/sessions", adminCookie, nil); !strings.Contains(w.Body.String(), `"principal_type": "admin"`) {
		t.Errorf("GET /v1/sessions as admin: %s", w.Body)
	}

	// Two sessions for the firm being suspended, one for another firm.
	managerSession := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}
	target1, target2 := m.sessionCookie(t, managerSession), m.sessionCookie(t, managerSession)
	otherID := uuid.New()
	other := m.sessionCookie(t, map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: otherID.String()})

	gomock.InOrder(
		m.store.EXPECT().AdminGetManager(gomock.Any(), testManagerID).
			Return(sqlc.AdminGetManagerRow{Manager: managerRow(data.ManagerStatusActive)}, nil),
		m.store.EXPECT().SetManagerStatus(gomock.Any(), sqlc.SetManagerStatusParams{
			ID: testManagerID, FromStatus: data.ManagerStatusActive, ToStatus: data.ManagerStatusSuspended,
		}).Return(int32(2), nil),
		m.store.EXPECT().AdminGetManager(gomock.Any(), testManagerID).
			Return(sqlc.AdminGetManagerRow{Manager: managerRow(data.ManagerStatusSuspended)}, nil),
	)

	w = m.send(http.MethodPost, "/v1/admin/managers/"+testManagerID.String()+"/suspend", "", adminCookie)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"sessions_revoked": 2`) ||
		!strings.Contains(body, `"status": "suspended"`) || !strings.Contains(body, `"subscription": null`) ||
		strings.Contains(body, "password") {
		t.Fatalf("suspend: status %d body %s", w.Code, body)
	}

	// The suspended firm's sessions are gone; the other firm's survives.
	for _, ck := range []*http.Cookie{target1, target2} {
		if w := m.do(http.MethodGet, "/v1/sessions", ck, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("revoked session still works: status %d", w.Code)
		}
	}
	m.store.EXPECT().GetManager(gomock.Any(), otherID).Return(sqlc.Manager{ID: otherID, Status: data.ManagerStatusActive}, nil)
	if w := m.do(http.MethodGet, "/v1/sessions", other, nil); w.Code != http.StatusOK {
		t.Errorf("another firm's session was revoked: status %d", w.Code)
	}

	// Manager credentials don't work at the admin login.
	m.store.EXPECT().GetAdminByEmail(gomock.Any(), "firm@example.com").Return(sqlc.Admin{}, sql.ErrNoRows)
	if w := m.send(http.MethodPost, "/v1/admin/sessions", loginBody("firm@example.com", testPassword), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("manager at admin login: status %d, want 401", w.Code)
	}
}

func TestBackupStatus(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }
	base := func(age time.Duration) *data.BackupRecord {
		return &data.BackupRecord{CompletedAt: now.Add(-age), SizeBytes: 1 << 30, Tool: "pgbackrest"}
	}
	wal := db.DatabaseHealth{WALArchivedCount: 10, LastArchivedTime: at(time.Hour)}

	tests := []struct {
		name string
		h    db.DatabaseHealth
		last *data.BackupRecord
		want string
	}{
		{"nothing yet", db.DatabaseHealth{}, nil, "not_configured"},
		{"fresh WAL and base backup", wal, base(2 * 24 * time.Hour), "healthy"},
		{"no base backup recorded", wal, nil, "stale"},
		{"base backup older than 8 days", wal, base(9 * 24 * time.Hour), "stale"},
		{"old WAL", db.DatabaseHealth{WALArchivedCount: 10, LastArchivedTime: at(48 * time.Hour)}, base(time.Hour), "stale"},
		{"failure after last success", db.DatabaseHealth{WALArchivedCount: 10, LastArchivedTime: at(2 * time.Hour), WALFailedCount: 1, LastFailedTime: at(time.Hour)}, base(time.Hour), "failing"},
		{"old failure, since recovered", db.DatabaseHealth{WALArchivedCount: 10, LastArchivedTime: at(time.Hour), WALFailedCount: 1, LastFailedTime: at(5 * time.Hour)}, base(time.Hour), "healthy"},
		{"only failures", db.DatabaseHealth{WALFailedCount: 3, LastFailedTime: at(time.Hour)}, nil, "failing"},
	}

	for _, tt := range tests {
		got := backupStatus(tt.h, tt.last, now)
		if got["status"] != tt.want {
			t.Errorf("%s: status = %v, want %s", tt.name, got["status"], tt.want)
		}
		if tt.last != nil && got["last_base_backup"].(*data.BackupRecord).SizeBytes != 1<<30 {
			t.Errorf("%s: base backup size not reported", tt.name)
		}
	}
}

func TestAdminReinstate(t *testing.T) {
	adminRow := sqlc.Admin{ID: testAdminID, Name: "Root"}
	adminSession := map[string]string{sessionKeyPrincipalType: principalAdmin, sessionKeyPrincipalID: testAdminID.String()}

	activatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name        string
		activatedAt *time.Time
		wantTo      string
	}{
		{"activated firm goes back to active", &activatedAt, data.ManagerStatusActive},
		{"never-activated firm goes back to pending", nil, data.ManagerStatusPending},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			m.store.EXPECT().GetAdmin(gomock.Any(), testAdminID).Return(adminRow, nil)

			suspended := managerRow(data.ManagerStatusSuspended)
			suspended.ActivatedAt = tt.activatedAt
			reinstated := suspended
			reinstated.Status = tt.wantTo

			gomock.InOrder(
				m.store.EXPECT().AdminGetManager(gomock.Any(), testManagerID).Return(sqlc.AdminGetManagerRow{Manager: suspended}, nil),
				m.store.EXPECT().SetManagerStatus(gomock.Any(), sqlc.SetManagerStatusParams{
					ID: testManagerID, FromStatus: data.ManagerStatusSuspended, ToStatus: tt.wantTo,
				}).Return(int32(3), nil),
				m.store.EXPECT().AdminGetManager(gomock.Any(), testManagerID).Return(sqlc.AdminGetManagerRow{Manager: reinstated}, nil),
			)

			w := m.send(http.MethodPost, "/v1/admin/managers/"+testManagerID.String()+"/reinstate", "", m.sessionCookie(t, adminSession))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "`+tt.wantTo+`"`) {
				t.Errorf("status %d body %s", w.Code, w.Body)
			}
		})
	}
}
