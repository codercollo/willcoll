package main

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

const resetCode = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func TestUpdateManagerPasswordHandler(t *testing.T) {
	t.Run("resets, burns every token, signs out every device", func(t *testing.T) {
		m := newMiddlewareTestApp(t)

		// The manager is signed in on two devices before resetting.
		managerSession := map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}
		laptop, phone := m.sessionCookie(t, managerSession), m.sessionCookie(t, managerSession)

		m.store.EXPECT().GetManagerForToken(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.GetManagerForTokenParams) (sqlc.Manager, error) {
				if arg.Scope != data.ScopePasswordReset {
					t.Errorf("token looked up in scope %q", arg.Scope)
				}
				return managerRow(data.ManagerStatusActive), nil
			})
		m.store.EXPECT().UpdateManager(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdateManagerParams) (int32, error) {
				if bcrypt.CompareHashAndPassword(arg.PasswordHash, []byte("a brand new passphrase")) != nil {
					t.Error("the new password was not what got saved")
				}
				if cost, _ := bcrypt.Cost(arg.PasswordHash); cost != 12 {
					t.Errorf("bcrypt cost = %d, want 12", cost)
				}
				return 2, nil
			})
		m.store.EXPECT().DeleteTokensForManager(gomock.Any(), testManagerID).Return(nil)

		w := m.send(http.MethodPut, "/v1/managers/password", `{"token":"`+resetCode+`","password":"a brand new passphrase"}`, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "successfully reset") {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}

		// Both devices are signed out.
		for _, ck := range []*http.Cookie{laptop, phone} {
			if w := m.do(http.MethodGet, "/v1/sessions", ck, nil); w.Code != http.StatusUnauthorized {
				t.Errorf("session survived the reset: status %d", w.Code)
			}
		}

		// The flash waits for the next response (e.g. the login page).
		flashCookie := cookieFrom(w)
		if flashCookie == nil {
			t.Fatal("reset response didn't carry the flash session")
		}
		if strings.Contains(w.Body.String(), "flash") {
			t.Error("the flash must not be shown on the reset response itself")
		}
		next := m.do(http.MethodGet, "/v1/healthcheck", flashCookie, nil)
		if !strings.Contains(next.Body.String(), "Your password has been reset") {
			t.Errorf("next response lacks the flash: %s", next.Body)
		}
	})

	t.Run("invalid or expired token", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetManagerForToken(gomock.Any(), gomock.Any()).Return(sqlc.Manager{}, sql.ErrNoRows)

		w := m.send(http.MethodPut, "/v1/managers/password", `{"token":"`+resetCode+`","password":"a brand new passphrase"}`, nil)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid or expired password reset token") {
			t.Errorf("status %d body %s", w.Code, w.Body)
		}
	})

	t.Run("weak password is rejected before the token is used", func(t *testing.T) {
		m := newMiddlewareTestApp(t) // no store expectations
		w := m.send(http.MethodPut, "/v1/managers/password", `{"token":"`+resetCode+`","password":"short"}`, nil)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"password"`) {
			t.Errorf("status %d body %s", w.Code, w.Body)
		}
	})

	t.Run("a reset is no way back into a suspended account", func(t *testing.T) {
		m := newMiddlewareTestApp(t)
		m.store.EXPECT().GetManagerForToken(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusSuspended), nil)

		w := m.send(http.MethodPut, "/v1/managers/password", `{"token":"`+resetCode+`","password":"a brand new passphrase"}`, nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("status %d, want 403", w.Code)
		}
	})
}
