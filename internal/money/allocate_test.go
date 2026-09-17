package money

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// seedUnitWithOpenInvoices creates one property/unit/tenant/lease with three
// open invoices: an arrears rent invoice (last month, KES 100), a current
// rent invoice (this month, KES 150), and a current water/garbage invoice
// (this month, KES 50) — total KES 300 (30,000 cents) of open dues, split
// across all three buckets AllocateManualPayment must order distinctly.
func seedUnitWithOpenInvoices(t *testing.T, orgID, userID uuid.UUID) (unitID uuid.UUID, arrearsInvoiceID, currentRentInvoiceID, waterInvoiceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	propertyID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO properties (id, organization_id, manager_id, name, location)
		VALUES ($1, $2, $3, $4, $5)`,
		propertyID, orgID, userID, "Property", "Test Location",
	); err != nil {
		t.Fatalf("insert property: %v", err)
	}

	unitID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO units (id, property_id, unit_label, base_rent, deposit_amount)
		VALUES ($1, $2, $3, $4, $5)`,
		unitID, propertyID, "A1", 15000, 15000,
	); err != nil {
		t.Fatalf("insert unit: %v", err)
	}

	tenantID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO tenants (id, organization_id, full_name, phone)
		VALUES ($1, $2, $3, $4)`,
		tenantID, orgID, "Tenant", "2547"+uuid.NewString()[:8],
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	leaseID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO leases (id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date, rent_due_day, late_fee_pct_per_day, status, created_by)
		VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, 5, 1.0, 'active', $6)`,
		leaseID, unitID, tenantID, 15000, 15000, userID,
	); err != nil {
		t.Fatalf("insert lease: %v", err)
	}

	arrearsInvoiceID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE - INTERVAL '1 month')::date, 'rent', 100)`,
		arrearsInvoiceID, orgID, leaseID,
	); err != nil {
		t.Fatalf("insert arrears invoice: %v", err)
	}

	currentRentInvoiceID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE)::date, 'rent', 150)`,
		currentRentInvoiceID, orgID, leaseID,
	); err != nil {
		t.Fatalf("insert current rent invoice: %v", err)
	}

	waterInvoiceID = uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE)::date, 'water_garbage', 50)`,
		waterInvoiceID, orgID, leaseID,
	); err != nil {
		t.Fatalf("insert water invoice: %v", err)
	}

	return unitID, arrearsInvoiceID, currentRentInvoiceID, waterInvoiceID
}

func TestAllocateManualPaymentOrdersArrearsThenCurrentThenWater(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	unitID, arrearsID, currentID, waterID := seedUnitWithOpenInvoices(t, orgID, userID)

	result, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitID,
		Amount:          30000, // KES 300 — exactly every open due
		Method:          "cash",
		ReferenceNumber: "REF-" + uuid.NewString(),
		RecordedBy:      userID,
	})
	if err != nil {
		t.Fatalf("AllocateManualPayment: %v", err)
	}

	if len(result.Allocations) != 3 {
		t.Fatalf("len(Allocations) = %d, want 3", len(result.Allocations))
	}
	wantOrder := []uuid.UUID{arrearsID, currentID, waterID}
	wantApplied := []int64{10000, 15000, 5000}
	for i, leg := range result.Allocations {
		if leg.InvoiceID != wantOrder[i] {
			t.Errorf("leg %d invoice = %s, want %s", i, leg.InvoiceID, wantOrder[i])
		}
		if leg.Applied != wantApplied[i] {
			t.Errorf("leg %d applied = %d, want %d", i, leg.Applied, wantApplied[i])
		}
	}
	if result.UnallocatedAmount != 0 {
		t.Fatalf("UnallocatedAmount = %d, want 0", result.UnallocatedAmount)
	}
}

func TestAllocateManualPaymentPartialStopsBeforeWater(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	unitID, arrearsID, currentID, _ := seedUnitWithOpenInvoices(t, orgID, userID)

	result, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitID,
		Amount:          20000, // KES 200 — clears arrears (100), partial current rent (100 of 150)
		Method:          "mpesa",
		ReferenceNumber: "REF-" + uuid.NewString(),
		RecordedBy:      userID,
	})
	if err != nil {
		t.Fatalf("AllocateManualPayment: %v", err)
	}

	if len(result.Allocations) != 2 {
		t.Fatalf("len(Allocations) = %d, want 2 (water untouched)", len(result.Allocations))
	}
	if result.Allocations[0].InvoiceID != arrearsID || result.Allocations[0].Applied != 10000 {
		t.Errorf("leg 0 = %+v, want arrears fully applied", result.Allocations[0])
	}
	if result.Allocations[1].InvoiceID != currentID || result.Allocations[1].Applied != 10000 {
		t.Errorf("leg 1 = %+v, want current rent partially applied (10000)", result.Allocations[1])
	}
	if result.UnallocatedAmount != 0 {
		t.Fatalf("UnallocatedAmount = %d, want 0", result.UnallocatedAmount)
	}
}

func TestAllocateManualPaymentOverpaymentIsFlaggedNotDropped(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	unitID, _, _, _ := seedUnitWithOpenInvoices(t, orgID, userID)

	result, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitID,
		Amount:          35000, // KES 350 — 50 more than every open due (300)
		Method:          "bank",
		ReferenceNumber: "REF-" + uuid.NewString(),
		RecordedBy:      userID,
	})
	if err != nil {
		t.Fatalf("AllocateManualPayment: %v", err)
	}

	if len(result.Allocations) != 3 {
		t.Fatalf("len(Allocations) = %d, want 3", len(result.Allocations))
	}
	if result.UnallocatedAmount != 5000 {
		t.Fatalf("UnallocatedAmount = %d, want 5000 (not silently dropped)", result.UnallocatedAmount)
	}
	if result.TotalAmount != 35000 {
		t.Fatalf("TotalAmount = %d, want 35000", result.TotalAmount)
	}
}

func TestAllocateManualPaymentNoOpenInvoicesReturnsFullyUnallocated(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)

	result, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          uuid.New(), // no invoices at all for this unit
		Amount:          10000,
		Method:          "cash",
		ReferenceNumber: "REF-" + uuid.NewString(),
		RecordedBy:      userID,
	})
	if err != nil {
		t.Fatalf("AllocateManualPayment: %v", err)
	}
	if len(result.Allocations) != 0 {
		t.Fatalf("len(Allocations) = %d, want 0", len(result.Allocations))
	}
	if result.UnallocatedAmount != 10000 {
		t.Fatalf("UnallocatedAmount = %d, want 10000", result.UnallocatedAmount)
	}
}

func TestAllocateManualPaymentDuplicateReferenceNumberReturnsCleanError(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	unitA, _, _, _ := seedUnitWithOpenInvoices(t, orgID, userID)
	unitB, _, _, _ := seedUnitWithOpenInvoices(t, orgID, userID)

	reference := "REF-" + uuid.NewString()

	if _, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitA,
		Amount:          10000,
		Method:          "cash",
		ReferenceNumber: reference,
		RecordedBy:      userID,
	}); err != nil {
		t.Fatalf("first AllocateManualPayment: %v", err)
	}

	_, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitB,
		Amount:          10000,
		Method:          "cash",
		ReferenceNumber: reference, // same receipt number reused on a different unit
		RecordedBy:      userID,
	})
	if !errors.Is(err, ErrDuplicateReferenceNumber) {
		t.Fatalf("second AllocateManualPayment error = %v, want ErrDuplicateReferenceNumber", err)
	}
}

func TestAllocateManualPaymentInvalidMethod(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	unitID, _, _, _ := seedUnitWithOpenInvoices(t, orgID, userID)

	_, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitID,
		Amount:          10000,
		Method:          "cheque", // not one of cash/mpesa/bank/card
		ReferenceNumber: "REF-" + uuid.NewString(),
		RecordedBy:      userID,
	})
	if !errors.Is(err, ErrManualPaymentMethodInvalid) {
		t.Fatalf("error = %v, want ErrManualPaymentMethodInvalid", err)
	}
}

func TestAllocateManualPaymentZeroAmount(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	unitID, _, _, _ := seedUnitWithOpenInvoices(t, orgID, userID)

	_, err := s.AllocateManualPayment(context.Background(), ManualPaymentInput{
		OrganizationID:  orgID,
		UnitID:          unitID,
		Amount:          0,
		Method:          "cash",
		ReferenceNumber: "REF-" + uuid.NewString(),
		RecordedBy:      userID,
	})
	if !errors.Is(err, ErrManualPaymentAmountInvalid) {
		t.Fatalf("error = %v, want ErrManualPaymentAmountInvalid", err)
	}
}
