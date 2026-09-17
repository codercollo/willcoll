package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/api"
	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}

	if err := db.RunMigrations(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	pool, err := db.NewPool(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open pool: %v\n", err)
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()

	pool.Close()
	os.Exit(code)
}

type recordedSend struct {
	recipient    string
	templateFile string
	data         any
}

type recordingMailer struct {
	mu    sync.Mutex
	sends []recordedSend
}

func (m *recordingMailer) Send(_ context.Context, recipient, templateFile string, data any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, recordedSend{
		recipient:    recipient,
		templateFile: templateFile,
		data:         data,
	})
	return nil
}

func (m *recordingMailer) lastToken(recipient string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := len(m.sends) - 1; i >= 0; i-- {
		s := m.sends[i]
		if s.recipient != recipient {
			continue
		}
		if data, ok := s.data.(map[string]string); ok {
			return data["Token"]
		}
	}
	return ""
}

type tokenResponse struct {
	Data struct {
		Token string `json:"token"`
	} `json:"data"`
}

type inviteResponse struct {
	Data struct {
		ID uuid.UUID `json:"id"`
	} `json:"data"`
}

func doJSON(t *testing.T, method, url, bearer string, body any) *http.Response {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func expectStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()

	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, want, b)
	}
}

func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

func TestManagerAgentActivationAndPasswordResetFlow(t *testing.T) {
	tenancySvc := tenancy.NewService(testPool)
	authSvc := auth.NewService([]byte("01234567890123456789012345678901"), testPool)
	mailSvc := &recordingMailer{}

	srv := api.NewServer(testPool, authSvc, tenancySvc, mailSvc, branding.NewService(testPool), money.NewService(testPool), nil, nil, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	managerEmail := "mgr-" + uuid.NewString() + "@example.com"
	managerPhone := uuid.NewString()
	agentEmail := "agt-" + uuid.NewString() + "@example.com"
	agentPhone := uuid.NewString()

	// Manager self-signup.
	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/auth/register-manager", "", map[string]string{
		"name":      "Org " + uuid.NewString(),
		"full_name": "Manager",
		"phone":     managerPhone,
		"email":     managerEmail,
		"password":  "managerpass",
	})
	expectStatus(t, resp, http.StatusCreated)
	var reg tokenResponse
	decodeBody(t, resp, &reg)
	managerToken := reg.Data.Token

	// Manager login issues a fresh token.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/auth/login", "", map[string]string{
		"email":    managerEmail,
		"password": "managerpass",
	})
	expectStatus(t, resp, http.StatusOK)
	var managerLogin tokenResponse
	decodeBody(t, resp, &managerLogin)
	managerToken = managerLogin.Data.Token

	// Manager invites an Agent.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/agents", managerToken, map[string]string{
		"full_name": "Agent",
		"phone":     agentPhone,
		"email":     agentEmail,
	})
	expectStatus(t, resp, http.StatusCreated)
	var invite inviteResponse
	decodeBody(t, resp, &invite)
	agentID := invite.Data.ID

	activationToken := mailSvc.lastToken(agentEmail)
	if activationToken == "" {
		t.Fatal("expected an activation email token for the invited agent")
	}

	// Agent activates and sets their first password.
	resp = doJSON(t, http.MethodPut, ts.URL+"/v1/users/activate", "", map[string]string{
		"token":    activationToken,
		"password": "agentpass",
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Agent logs in with the new password.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/auth/login", "", map[string]string{
		"email":    agentEmail,
		"password": "agentpass",
	})
	expectStatus(t, resp, http.StatusOK)
	var agentLogin tokenResponse
	decodeBody(t, resp, &agentLogin)
	if agentLogin.Data.Token == "" {
		t.Fatal("expected agent login to return a token")
	}

	// Manager triggers a password reset.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/agents/"+agentID.String()+"/reset-password", managerToken, nil)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resetToken := mailSvc.lastToken(agentEmail)
	if resetToken == "" {
		t.Fatal("expected a password reset email token for the agent")
	}

	// Agent completes the reset.
	resp = doJSON(t, http.MethodPut, ts.URL+"/v1/users/password", "", map[string]string{
		"token":        resetToken,
		"new_password": "newagentpass",
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// New password works.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/auth/login", "", map[string]string{
		"email":    agentEmail,
		"password": "newagentpass",
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Old password no longer works.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/auth/login", "", map[string]string{
		"email":    agentEmail,
		"password": "agentpass",
	})
	expectStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}
