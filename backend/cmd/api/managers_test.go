package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"
)

// fakeMailer records sent emails instead of sending them.
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentEmail
}

type sentEmail struct {
	recipient, template string
	data                map[string]any
}

func (m *fakeMailer) Send(recipient, templateFile string, data any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, _ := data.(map[string]any)
	m.sent = append(m.sent, sentEmail{recipient, templateFile, d})
	return nil
}

func newAuthTestApp(t *testing.T) (*application, *mock.MockStore, *fakeMailer) {
	t.Helper()
	store := mock.NewMockStore(gomock.NewController(t))
	mailer := &fakeMailer{}
	app := &application{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		models: data.NewModelsFromStore(store, 0),
		mailer: mailer,
	}
	app.config.frontendURL = "http://localhost:3000"
	return app, store, mailer
}

func serve(handler gin.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	handler(c)
	return w
}

const registerBody = `{
	"firm_name": "Runda Estates", "username": "runda", "email": "office@runda.co.ke",
	"phone": "+254712345678", "password": "correct horse battery"
}`

func TestRegisterManagerHandler(t *testing.T) {
	t.Run("registers pending and emails an activation code", func(t *testing.T) {
		app, store, mailer := newAuthTestApp(t)

		store.EXPECT().CreateManager(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.CreateManagerParams) (sqlc.Manager, error) {
				if arg.Status != data.ManagerStatusPending || len(arg.PasswordHash) == 0 || string(arg.PasswordHash) == "correct horse battery" {
					t.Errorf("unexpected insert: status %q, hash %q", arg.Status, arg.PasswordHash)
				}
				return sqlc.Manager{ID: testManagerID, Version: 1}, nil
			})
		var stored sqlc.CreateTokenParams
		store.EXPECT().CreateToken(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.CreateTokenParams) error { stored = arg; return nil })

		w := serve(app.registerManagerHandler, http.MethodPost, registerBody)
		app.wg.Wait() // the email goes out in the background

		if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status": "pending"`) {
			t.Fatalf("status = %d body = %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "password") {
			t.Error("response must not include the password or its hash")
		}
		if stored.Scope != data.ScopeActivation || stored.ManagerID != testManagerID || time.Until(stored.Expiry) < 71*time.Hour {
			t.Errorf("stored token = %+v", stored)
		}

		if len(mailer.sent) != 1 {
			t.Fatalf("sent %d emails, want 1", len(mailer.sent))
		}
		email := mailer.sent[0]
		code, _ := email.data["ActivationToken"].(string)
		if email.recipient != "office@runda.co.ke" || email.template != "manager_welcome.tmpl" || len(code) != 26 ||
			email.data["ActivationURL"] != "http://localhost:3000/auth/activate?token="+code {
			t.Errorf("email = %+v", email)
		}
		if string(stored.Hash) == code {
			t.Error("the token must be stored hashed, never in plaintext")
		}
	})

	t.Run("duplicate email", func(t *testing.T) {
		app, store, mailer := newAuthTestApp(t)
		store.EXPECT().CreateManager(gomock.Any(), gomock.Any()).
			Return(sqlc.Manager{}, &pgconn.PgError{Code: "23505", ConstraintName: "managers_email_key"})

		w := serve(app.registerManagerHandler, http.MethodPost, registerBody)
		app.wg.Wait()
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "already exists") || len(mailer.sent) != 0 {
			t.Errorf("status = %d body = %s, emails %d", w.Code, w.Body, len(mailer.sent))
		}
	})

	t.Run("invalid input is rejected before hashing or saving", func(t *testing.T) {
		app, _, _ := newAuthTestApp(t) // no store expectations: nothing may be saved
		w := serve(app.registerManagerHandler, http.MethodPost,
			`{"firm_name":"F","username":"u","email":"nope","phone":"0712","password":"short"}`)
		for _, want := range []string{`"email"`, `"phone"`, `"password": "must be at least 8 bytes long"`} {
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), want) {
				t.Errorf("status = %d body = %s; want 422 with %s", w.Code, w.Body, want)
			}
		}
	})

	t.Run("password over 72 bytes", func(t *testing.T) {
		app, _, _ := newAuthTestApp(t)
		long := strings.Repeat("ü", 40) // 40 characters, 80 bytes
		w := serve(app.registerManagerHandler, http.MethodPost, strings.Replace(registerBody, "correct horse battery", long, 1))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "72 bytes") {
			t.Errorf("status = %d body = %s; want 422 (not a bcrypt 500)", w.Code, w.Body)
		}
	})
}

func TestActivateManagerHandler(t *testing.T) {
	const code = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	t.Run("activates and burns the token", func(t *testing.T) {
		app, store, _ := newAuthTestApp(t)

		store.EXPECT().GetManagerForToken(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.GetManagerForTokenParams) (sqlc.Manager, error) {
				if arg.Scope != data.ScopeActivation || string(arg.Hash) == code {
					t.Errorf("token must be looked up hashed, in activation scope: %+v", arg)
				}
				return managerRow(data.ManagerStatusPending), nil
			})
		store.EXPECT().UpdateManager(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdateManagerParams) (int32, error) {
				if arg.Status != data.ManagerStatusActive || arg.ActivatedAt == nil || arg.Version != 1 {
					t.Errorf("unexpected update %+v", arg)
				}
				return 2, nil
			})
		store.EXPECT().DeleteAllTokensForManager(gomock.Any(), sqlc.DeleteAllTokensForManagerParams{
			Scope: data.ScopeActivation, ManagerID: testManagerID,
		}).Return(nil)

		w := serve(app.activateManagerHandler, http.MethodPut, `{"token": "`+code+`"}`)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "active"`) || !strings.Contains(w.Body.String(), "activated_at") {
			t.Errorf("status = %d body = %s", w.Code, w.Body)
		}
	})

	t.Run("unknown or expired token", func(t *testing.T) {
		app, store, _ := newAuthTestApp(t)
		store.EXPECT().GetManagerForToken(gomock.Any(), gomock.Any()).Return(sqlc.Manager{}, sql.ErrNoRows)

		w := serve(app.activateManagerHandler, http.MethodPut, `{"token": "`+code+`"}`)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid or expired activation token") {
			t.Errorf("status = %d body = %s", w.Code, w.Body)
		}
	})

	t.Run("malformed token never reaches the database", func(t *testing.T) {
		app, _, _ := newAuthTestApp(t)
		w := serve(app.activateManagerHandler, http.MethodPut, `{"token": "short"}`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", w.Code)
		}
	})

	t.Run("activation doesn't lift a suspension", func(t *testing.T) {
		app, store, _ := newAuthTestApp(t)
		store.EXPECT().GetManagerForToken(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusSuspended), nil)

		if w := serve(app.activateManagerHandler, http.MethodPut, `{"token": "`+code+`"}`); w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})
}

func TestCreateActivationTokenHandler(t *testing.T) {
	const body = `{"email": "firm@example.com"}`

	for _, tt := range []struct {
		name  string
		setup func(store *mock.MockStore)
		sends int
	}{
		{"unknown email", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), "firm@example.com").Return(sqlc.Manager{}, sql.ErrNoRows)
		}, 0},
		{"already active", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusActive), nil)
		}, 0},
		{"pending: old codes revoked, new one sent", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusPending), nil)
			gomock.InOrder(
				store.EXPECT().DeleteAllTokensForManager(gomock.Any(), sqlc.DeleteAllTokensForManagerParams{Scope: data.ScopeActivation, ManagerID: testManagerID}).Return(nil),
				store.EXPECT().CreateToken(gomock.Any(), gomock.Any()).Return(nil),
			)
		}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, store, mailer := newAuthTestApp(t)
			tt.setup(store)

			w := serve(app.createActivationTokenHandler, http.MethodPost, body)
			app.wg.Wait()

			// Same response either way, so the endpoint can't reveal which
			// email addresses are registered.
			if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "if an unactivated account uses this email address") {
				t.Errorf("status = %d body = %s", w.Code, w.Body)
			}
			if len(mailer.sent) != tt.sends {
				t.Errorf("sent %d emails, want %d", len(mailer.sent), tt.sends)
			}
		})
	}
}

func TestCreatePasswordResetTokenHandler(t *testing.T) {
	const body = `{"email": "firm@example.com"}`

	for _, tt := range []struct {
		name  string
		setup func(store *mock.MockStore)
		sends int
	}{
		{"unknown email", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(sqlc.Manager{}, sql.ErrNoRows)
		}, 0},
		{"pending account", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusPending), nil)
		}, 0},
		{"suspended account", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusSuspended), nil)
		}, 0},
		{"active: old reset codes revoked, new one sent", func(store *mock.MockStore) {
			store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(managerRow(data.ManagerStatusActive), nil)
			gomock.InOrder(
				store.EXPECT().DeleteAllTokensForManager(gomock.Any(), sqlc.DeleteAllTokensForManagerParams{Scope: data.ScopePasswordReset, ManagerID: testManagerID}).Return(nil),
				store.EXPECT().CreateToken(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, arg sqlc.CreateTokenParams) error {
						if arg.Scope != data.ScopePasswordReset || time.Until(arg.Expiry) > 46*time.Minute || time.Until(arg.Expiry) < 44*time.Minute {
							t.Errorf("reset token = scope %q, expires in %s; want password-reset, 45m", arg.Scope, time.Until(arg.Expiry))
						}
						return nil
					}),
			)
		}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, store, mailer := newAuthTestApp(t)
			tt.setup(store)

			w := serve(app.createPasswordResetTokenHandler, http.MethodPost, body)
			app.wg.Wait()

			if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "if an active account uses this email address") {
				t.Errorf("status = %d body = %s", w.Code, w.Body)
			}
			if len(mailer.sent) != tt.sends {
				t.Fatalf("sent %d emails, want %d", len(mailer.sent), tt.sends)
			}
			if tt.sends == 1 {
				email := mailer.sent[0]
				code, _ := email.data["ResetToken"].(string)
				if email.template != "password_reset.tmpl" || len(code) != 26 ||
					email.data["ResetURL"] != "http://localhost:3000/auth/reset-password?token="+code || email.data["ExpiresIn"] != "45 minutes" {
					t.Errorf("email = %+v", email)
				}
			}
		})
	}
}

// Greenlight ch.13.5: serve()'s shutdown calls app.wg.Wait(), so a
// background task started before shutdown finishes before the process
// exits, and a panicking task can't take the server down or leak wg.
func TestBackgroundTasksAndGracefulShutdown(t *testing.T) {
	app, _, _ := newAuthTestApp(t)

	var finished atomic.Bool
	app.background(func() {
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
	})
	app.background(func() { panic("boom") })

	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if !finished.Load() {
			t.Error("wg.Wait returned before the background task finished")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wg.Wait never returned: a background task leaked the WaitGroup")
	}
}
