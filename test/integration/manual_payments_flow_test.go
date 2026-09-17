package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/api"
	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
)

type allocationLeg struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	InvoiceType string    `json:"invoice_type"`
	Applied     int64     `json:"applied"`
}

type manualPaymentResponse struct {
	Data struct {
		UnitID            uuid.UUID       `json:"unit_id"`
		TotalAmount       int64           `json:"total_amount"`
		Allocations       []allocationLeg `json:"allocations"`
		UnallocatedAmount int64           `json:"unallocated_amount"`
	} `json:"data"`
}

// TestManualPaymentRecordingEndToEnd exercises POST /v1/payments' Manual
// Payment Recording path (Manual Payment Recording brief, phase 3
// deliverable): a Manager records cash against a unit with an arrears
// invoice and a current rent invoice, expects the allocation breakdown back
// split arrears-then-current, and a second call reusing the same
// reference_number is rejected 409, not 500.
func TestManualPaymentRecordingEndToEnd(t *testing.T) {
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Manual Payments Org", "Manual Payments Org", "manual-pay-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert org: %v", err)
	}

	managerID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
		VALUES ($1, $2, 'Manager', $3, $4, 'manager', true, true, 'active')`,
		managerID, orgID, uuid.NewString(), "mgr-"+uuid.NewString()+"@example.com",
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

	unitID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO units (id, property_id, unit_label, base_rent, deposit_amount)
		VALUES ($1, $2, 'A1', 15000, 15000)`,
		unitID, propertyID,
	); err != nil {
		t.Fatalf("insert unit: %v", err)
	}

	tenantID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO tenants (id, organization_id, full_name, phone)
		VALUES ($1, $2, 'Tenant', $3)`,
		tenantID, orgID, "2547"+uuid.NewString()[:8],
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	leaseID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO leases (id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date, rent_due_day, late_fee_pct_per_day, status, created_by)
		VALUES ($1, $2, $3, 15000, 15000, CURRENT_DATE, 5, 1.0, 'active', $4)`,
		leaseID, unitID, tenantID, managerID,
	); err != nil {
		t.Fatalf("insert lease: %v", err)
	}

	arrearsID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE - INTERVAL '1 month')::date, 'rent', 100)`,
		arrearsID, orgID, leaseID,
	); err != nil {
		t.Fatalf("insert arrears invoice: %v", err)
	}

	currentID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE)::date, 'rent', 150)`,
		currentID, orgID, leaseID,
	); err != nil {
		t.Fatalf("insert current invoice: %v", err)
	}

	authSvc := auth.NewService([]byte("01234567890123456789012345678901"), testPool)
	token, err := authSvc.IssueToken(managerID, "manager", orgID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	srv := api.NewServer(
		testPool,
		authSvc,
		tenancy.NewService(testPool),
		&recordingMailer{},
		branding.NewService(testPool),
		money.NewService(testPool),
		nil,
		nil,
		nil,
		nil,
	)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reference := "MPESA-" + uuid.NewString()
	body := map[string]any{
		"unit_id":          unitID,
		"amount":           20000, // KES 200: clears the 100 arrears, 100 of the 150 current rent
		"payment_method":   "mpesa",
		"reference_number": reference,
		"notes":            "handed over at the office",
	}

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/payments", token, body)
	expectStatus(t, resp, http.StatusCreated)

	var out manualPaymentResponse
	decodeBody(t, resp, &out)

	if len(out.Data.Allocations) != 2 {
		t.Fatalf("len(Allocations) = %d, want 2", len(out.Data.Allocations))
	}
	if out.Data.Allocations[0].InvoiceID != arrearsID || out.Data.Allocations[0].Applied != 10000 {
		t.Errorf("leg 0 = %+v, want arrears fully applied", out.Data.Allocations[0])
	}
	if out.Data.Allocations[1].InvoiceID != currentID || out.Data.Allocations[1].Applied != 10000 {
		t.Errorf("leg 1 = %+v, want current rent partially applied (10000)", out.Data.Allocations[1])
	}
	if out.Data.UnallocatedAmount != 0 {
		t.Errorf("UnallocatedAmount = %d, want 0", out.Data.UnallocatedAmount)
	}

	var auditCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_log
		WHERE organization_id = $1 AND action = 'manual_payment.recorded' AND entity_id = $2`,
		orgID, unitID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit_log rows for this payment = %d, want 1", auditCount)
	}

	// Same reference number reused → clean 409, not a generic 500.
	dupResp := doJSON(t, http.MethodPost, ts.URL+"/v1/payments", token, body)
	expectStatus(t, dupResp, http.StatusConflict)
}
