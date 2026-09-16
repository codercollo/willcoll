package integration

import (
	"context"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/billing"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/google/uuid"
)

func seedLateFeeInvoice(t *testing.T) (orgID, userID, tenantID, propertyID, invoiceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	orgID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Late Fee Org", "Late Fee Org", "latefee-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	userID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
		VALUES ($1, $2, $3, $4, $5, 'manager', true, true, 'active')`,
		userID, orgID, "Manager", uuid.NewString(), "latefee-"+uuid.NewString()+"@example.com",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	propertyID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO properties (id, organization_id, manager_id, name, location)
		VALUES ($1, $2, $3, $4, $5)`,
		propertyID, orgID, userID, "Property", "Test Location",
	); err != nil {
		t.Fatalf("insert property: %v", err)
	}

	unitID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO units (id, property_id, unit_label, base_rent, deposit_amount)
		VALUES ($1, $2, $3, $4, $5)`,
		unitID, propertyID, "A1", 10000, 10000,
	); err != nil {
		t.Fatalf("insert unit: %v", err)
	}

	tenantID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO tenants (id, organization_id, full_name, phone)
		VALUES ($1, $2, $3, $4)`,
		tenantID, orgID, "Tenant", "254700000000",
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	leaseID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO leases (id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date, rent_due_day, late_fee_pct_per_day, status, created_by)
		VALUES ($1, $2, $3, 10000, 10000, date_trunc('month', CURRENT_DATE - interval '2 months')::date, 1, 1.0, 'active', $4)`,
		leaseID, unitID, tenantID, userID,
	); err != nil {
		t.Fatalf("insert lease: %v", err)
	}

	invoiceID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE - interval '2 months')::date, 'rent', 10000)`,
		invoiceID, orgID, leaseID,
	); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}

	return orgID, userID, tenantID, propertyID, invoiceID
}

func TestChargeLateFeesPostsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	moneySvc := money.NewService(testPool)
	billingSvc := billing.NewService(moneySvc, testPool, nil)

	_, _, _, _, invoiceID := seedLateFeeInvoice(t)

	first, err := billingSvc.ChargeLateFees(ctx)
	if err != nil {
		t.Fatalf("first ChargeLateFees: %v", err)
	}
	if first.InvoicesCharged == 0 {
		t.Fatal("expected at least one late fee charge")
	}

	second, err := billingSvc.ChargeLateFees(ctx)
	if err != nil {
		t.Fatalf("second ChargeLateFees: %v", err)
	}
	if second.InvoicesCharged == 0 {
		t.Fatal("expected late fee candidate on second run")
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM ledger_transfers
		WHERE invoice_id = $1 AND transfer_type = 'late_fee_charge'`,
		invoiceID,
	).Scan(&count); err != nil {
		t.Fatalf("count late fee transfers: %v", err)
	}
	if count != 1 {
		t.Fatalf("late fee transfer count = %d, want 1", count)
	}
}
