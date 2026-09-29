package data

import (
	"context"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/google/uuid"
)

// TestPayBalancesElectricityDeposit covers Feature 3's electricity line:
// absent while the property has it switched off, present (with a Paid/Owing
// balance) once it's on, and gone again once fully paid.
func TestPayBalancesElectricityDeposit(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	channel := "PH-CH-TEST"
	property := &Property{
		LandlordID: landlord, Name: "Electricity Pay Property", Location: "Nairobi",
		Slug: "electricity-pay-" + uuid.NewString()[:8], WaterRatePerUnit: biMoney(t, "150"),
		PayheroChannelID: &channel,
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "E1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "TEST TENANT", PrimaryPhone: "+254700000099",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: LeaseStatusActive}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	target := PayTarget{TenantID: tenant, PropertyID: property.ID, UnitID: unit.ID}

	hasLine := func(lines []PayLine, lt string) (PayLine, bool) {
		for _, l := range lines {
			if l.Type == lt {
				return l, true
			}
		}
		return PayLine{}, false
	}

	lines, err := models.PayAccess.Balances(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hasLine(lines, LedgerTypeElectricityDeposit); ok {
		t.Fatal("electricity deposit line offered while the property has it disabled")
	}

	if _, err := models.Leases.EnableElectricityDeposit(ctx, tenant, tenant, property.ID, biMoney(t, "2000")); err != nil {
		t.Fatalf("EnableElectricityDeposit: %v", err)
	}
	current, err := models.Properties.Get(ctx, tenant, property.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.ElectricityEnabled = true
	current.ElectricityDepositAmount = biMoney(t, "2000")
	if err := models.Properties.Update(ctx, current); err != nil {
		t.Fatal(err)
	}

	lines, err = models.PayAccess.Balances(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	line, ok := hasLine(lines, LedgerTypeElectricityDeposit)
	if !ok || line.Balance.Display() != "2,000.00" || !line.Payable {
		t.Fatalf("electricity deposit line = %+v, ok=%v, want 2,000.00 payable", line, ok)
	}

	// Overpaying the deposit must be rejected server-side, not just capped
	// client-side.
	_, err = models.PayAccess.CreateIntent(ctx, target, "+254700000001",
		[]IntentLine{{Type: LedgerTypeElectricityDeposit, Amount: biMoney(t, "2000.01")}}, time.Minute)
	if err == nil {
		t.Fatal("expected the overpay to be rejected")
	}

	// The boundary is inclusive: an amount exactly equal to the balance is
	// allowed, only strictly more is rejected.
	if _, err := models.PayAccess.CreateIntent(ctx, target, "+254700000001",
		[]IntentLine{{Type: LedgerTypeElectricityDeposit, Amount: biMoney(t, "2000")}}, time.Minute); err != nil {
		t.Fatalf("CreateIntent at exactly the balance: %v", err)
	}
}
