package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codercollo/willcoll-sys/internal/api"
	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/scoring"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
)

type scoreResponseBody struct {
	Data struct {
		ScoreBand string `json:"score_band"`
	} `json:"data"`
}

// TestVerifiedPropertyScoreFlowEndToEnd proves POST /v1/properties/:id/score
// is blocked (402) until the add-on is activated, then succeeds and
// persists once it is — against a stub ml-sidecar (spec §13, Go side only).
func TestVerifiedPropertyScoreFlowEndToEnd(t *testing.T) {
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Score Org", "Score Org", "score-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert org: %v", err)
	}

	managerID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
		VALUES ($1, $2, 'Manager', $3, $4, 'manager', true, true, 'active')`,
		managerID, orgID, uuid.NewString(), "score-mgr-"+uuid.NewString()+"@example.com",
	); err != nil {
		t.Fatalf("insert manager: %v", err)
	}

	propertyID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO properties (id, organization_id, manager_id, name, location)
		VALUES ($1, $2, $3, 'Property', 'Test Location')`,
		propertyID, orgID, managerID,
	); err != nil {
		t.Fatalf("insert property: %v", err)
	}

	authSvc := auth.NewService([]byte("01234567890123456789012345678901"), testPool)
	token, err := authSvc.IssueToken(managerID, "manager", orgID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	// Stubs the sidecar's own INSERT into property_scores (spec §13.3.1:
	// the sidecar owns that write, not this Go client) so the later GET
	// actually finds a row.
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sidecar-Shared-Secret") != "test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var scoreID uuid.UUID
		var computedAt time.Time
		if err := testPool.QueryRow(ctx, `
			INSERT INTO property_scores (
				organization_id, property_id, as_of_date, model_version, noi, dscr,
				score_value, score_band, feature_snapshot, requested_by
			) VALUES ($1, $2, CURRENT_DATE, 'v1', 1000, 1.5, 72.5, 'B', '{}', $3)
			RETURNING id, computed_at`,
			orgID, propertyID, managerID,
		).Scan(&scoreID, &computedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": scoreID, "organization_id": orgID, "property_id": propertyID,
			"computed_at": computedAt.Format(time.RFC3339), "as_of_date": "2026-01-01", "model_version": "v1",
			"noi": "1000.00", "dscr": "1.5", "score_value": "72.5", "score_band": "B",
			"requested_by": managerID,
		})
	}))
	defer sidecar.Close()

	scoringSvc := scoring.NewService(testPool, sidecar.URL, "test-secret")
	srv := api.NewServer(
		testPool, authSvc, tenancy.NewService(testPool), &recordingMailer{},
		branding.NewService(testPool), money.NewService(testPool), nil, nil, nil, scoringSvc,
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	scoreBody := map[string]any{"operating_expenses": "40000", "annual_debt_service": "50000"}

	// Not activated yet → 402.
	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/properties/"+propertyID.String()+"/score", token, scoreBody)
	expectStatus(t, resp, http.StatusPaymentRequired)

	// Activate, then request succeeds and persists.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/organization/addons/verified-property-score/activate", token, map[string]any{
		"monthly_fee_kes": 500,
	})
	expectStatus(t, resp, http.StatusOK)

	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/properties/"+propertyID.String()+"/score", token, scoreBody)
	expectStatus(t, resp, http.StatusCreated)
	var out scoreResponseBody
	decodeBody(t, resp, &out)
	if out.Data.ScoreBand != "B" {
		t.Fatalf("score_band = %q, want %q", out.Data.ScoreBand, "B")
	}

	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/properties/"+propertyID.String()+"/score", token, nil)
	expectStatus(t, resp, http.StatusOK)
}
