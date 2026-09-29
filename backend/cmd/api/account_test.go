package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

// accountRequest sends a JSON request through the real router, session
// middleware and auth included.
func (m *middlewareTestApp) accountRequest(method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	m.router.ServeHTTP(w, req)
	return w
}

func accountManagerRow(t *testing.T, password string) sqlc.Manager {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	row := managerRow("active")
	row.PasswordHash = hash
	return row
}

func TestAccountProfile(t *testing.T) {
	session := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}

	t.Run("needs a session", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		if w := m.accountRequest(http.MethodGet, "/v1/account", "", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
	})

	t.Run("shows the firm, never the password", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(accountManagerRow(t, "old-password-1"), nil)
		w := m.accountRequest(http.MethodGet, "/v1/account", "", m.sessionCookie(t, session))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"firm_name": "Firm"`) {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if strings.Contains(strings.ToLower(w.Body.String()), "password") || strings.Contains(w.Body.String(), "hash") {
			t.Errorf("the account response leaks credentials: %s", w.Body)
		}
	})

	t.Run("edits the firm name and phone; omitted fields stay", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(accountManagerRow(t, "old-password-1"), nil)
		m.store.EXPECT().UpdateManager(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.UpdateManagerParams) (int32, error) {
				if a.FirmName != "Acme Managers" || a.Phone != "+254700000000" || a.Email != "firm@example.com" || a.Version != 1 {
					t.Errorf("update = %+v (only the firm name changes)", a)
				}
				return 2, nil
			})
		w := m.accountRequest(http.MethodPatch, "/v1/account", `{"firm_name":"  Acme Managers "}`, m.sessionCookie(t, session))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Acme Managers") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("rejects a blank name and a bad phone before writing", func(t *testing.T) {
		for body, want := range map[string]string{`{"firm_name":"  "}`: "firm_name", `{"phone":"0700"}`: "phone"} {
			m := newMiddlewareTestApp(t)
			m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(accountManagerRow(t, "old-password-1"), nil)
			w := m.accountRequest(http.MethodPatch, "/v1/account", body, m.sessionCookie(t, session))
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), want) {
				t.Errorf("%s: status %d: %s", body, w.Code, w.Body)
			}
		}
	})
}

func TestChangePassword(t *testing.T) {
	session := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}
	change := func(m *middlewareTestApp, body string) *httptest.ResponseRecorder {
		m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(accountManagerRow(t, "old-password-1"), nil).AnyTimes()
		return m.accountRequest(http.MethodPut, "/v1/account/password", body, m.sessionCookie(t, session))
	}

	rejected := map[string]struct{ body, want string }{
		"wrong current password":   {`{"current_password":"not-it","new_password":"brand-new-pass"}`, "is incorrect"},
		"unchanged password":       {`{"current_password":"old-password-1","new_password":"old-password-1"}`, "must be different"},
		"too short":                {`{"current_password":"old-password-1","new_password":"short"}`, "at least 8"},
		"too long for bcrypt":      {`{"current_password":"old-password-1","new_password":"` + strings.Repeat("x", 73) + `"}`, "not be more than 72"},
		"no current password":      {`{"new_password":"brand-new-pass"}`, "current_password"},
		"the username as password": {`{"current_password":"old-password-1","new_password":"firm"}`, "at least 8"},
	}
	for name, tc := range rejected {
		t.Run("refuses "+name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			w := change(m, tc.body) // no UpdateManager expectation: nothing may be written
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("status %d: %s (want 422 containing %q)", w.Code, w.Body, tc.want)
			}
		})
	}

	t.Run("changes it, signs other devices out, keeps this one in", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		var newHash []byte
		m.store.EXPECT().UpdateManager(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.UpdateManagerParams) (int32, error) { newHash = a.PasswordHash; return 2, nil })
		m.store.EXPECT().DeleteTokensForManager(gomock.Any(), testManagerID).Return(nil)

		// A second device, signed in before the change.
		otherDevice := m.sessionCookie(t, session)
		this := m.sessionCookie(t, session)

		m.store.EXPECT().GetManager(gomock.Any(), testManagerID).Return(accountManagerRow(t, "old-password-1"), nil).AnyTimes()
		w := m.accountRequest(http.MethodPut, "/v1/account/password", `{"current_password":"old-password-1","new_password":"brand-new-pass"}`, this)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if err := bcrypt.CompareHashAndPassword(newHash, []byte("brand-new-pass")); err != nil {
			t.Errorf("the stored hash does not match the new password: %v", err)
		}
		if bcrypt.CompareHashAndPassword(newHash, []byte("old-password-1")) == nil {
			t.Error("the stored hash still matches the old password")
		}

		// The other device is out; the device that changed it has a fresh session.
		if w := m.accountRequest(http.MethodGet, "/v1/account", "", otherDevice); w.Code != http.StatusUnauthorized {
			t.Errorf("the other device after the change = %d, want 401", w.Code)
		}
		if w := m.accountRequest(http.MethodGet, "/v1/account", "", this); w.Code != http.StatusUnauthorized {
			t.Errorf("the pre-change cookie must be dead, got %d", w.Code)
		}
		fresh := sessionCookieFrom(t, w)
		if fresh.Value == "" || fresh.Value == this.Value {
			t.Fatalf("no fresh session was issued: %+v", fresh)
		}
		if w := m.accountRequest(http.MethodGet, "/v1/account", "", fresh); w.Code != http.StatusOK {
			t.Errorf("the device that changed the password was signed out: status %d", w.Code)
		}
	})
}
