package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/api"
	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
)

type propertyCreateResponse struct {
	Data struct {
		ID uuid.UUID `json:"id"`
	} `json:"data"`
}

type unitCreateResponse struct {
	Data struct {
		ID uuid.UUID `json:"id"`
	} `json:"data"`
}

func createPropertyForPBAC(t *testing.T, ts *httptest.Server, token, name string) uuid.UUID {
	t.Helper()

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/properties", token, map[string]string{
		"name":     name,
		"location": "Test Location",
	})
	expectStatus(t, resp, http.StatusCreated)
	var out propertyCreateResponse
	decodeBody(t, resp, &out)
	return out.Data.ID
}

func createUnitForPBAC(t *testing.T, ts *httptest.Server, token string, propertyID uuid.UUID, label string) uuid.UUID {
	t.Helper()

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/properties/"+propertyID.String()+"/units", token, map[string]any{
		"unit_label":     label,
		"unit_type":      "residential",
		"base_rent":      10000,
		"deposit_amount": 10000,
	})
	expectStatus(t, resp, http.StatusCreated)
	var out unitCreateResponse
	decodeBody(t, resp, &out)
	return out.Data.ID
}

func TestAgentGrantIsScopedPerProperty(t *testing.T) {
	tenancySvc := tenancy.NewService(testPool)
	authSvc := auth.NewService([]byte("01234567890123456789012345678901"))
	mailSvc := &recordingMailer{}

	srv := api.NewServer(testPool, authSvc, tenancySvc, mailSvc, branding.NewService(testPool), money.NewService(testPool), nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	managerEmail := "pbac-mgr-" + uuid.NewString() + "@example.com"
	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/auth/register-manager", "", map[string]string{
		"name":      "PBAC Org " + uuid.NewString(),
		"full_name": "Manager",
		"phone":     uuid.NewString(),
		"email":     managerEmail,
		"password":  "managerpass",
	})
	expectStatus(t, resp, http.StatusCreated)
	var reg tokenResponse
	decodeBody(t, resp, &reg)
	managerToken := reg.Data.Token

	propertyA := createPropertyForPBAC(t, ts, managerToken, "Property A")
	propertyB := createPropertyForPBAC(t, ts, managerToken, "Property B")
	unitB := createUnitForPBAC(t, ts, managerToken, propertyB, "B1")

	agentEmail := "pbac-agt-" + uuid.NewString() + "@example.com"
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/agents", managerToken, map[string]string{
		"full_name": "Agent",
		"phone":     uuid.NewString(),
		"email":     agentEmail,
	})
	expectStatus(t, resp, http.StatusCreated)
	var invite inviteResponse
	decodeBody(t, resp, &invite)
	agentID := invite.Data.ID

	activationToken := mailSvc.lastToken(agentEmail)
	if activationToken == "" {
		t.Fatal("expected activation token for agent")
	}

	resp = doJSON(t, http.MethodPut, ts.URL+"/v1/users/activate", "", map[string]string{
		"token":    activationToken,
		"password": "agentpass",
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/auth/login", "", map[string]string{
		"email":    agentEmail,
		"password": "agentpass",
	})
	expectStatus(t, resp, http.StatusOK)
	var agentLogin tokenResponse
	decodeBody(t, resp, &agentLogin)
	agentToken := agentLogin.Data.Token

	// Grant the Agent lease-edit permission ONLY on Property A.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/agent-grants", managerToken, map[string]any{
		"agent_id":        agentID,
		"property_id":     propertyA,
		"can_edit_leases": true,
	})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// The Agent tries to create a lease on Property B and must be denied.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/units/"+unitB.String()+"/leases", agentToken, map[string]any{
		"full_name":    "Tenant B",
		"phone":        "254700000000",
		"monthly_rent": 10000,
		"deposit_paid": 10000,
		"start_date":   "2026-01-01",
	})
	expectStatus(t, resp, http.StatusForbidden)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "not granted for this property") {
		t.Fatalf("expected PBAC denial message, got: %s", body)
	}
}
