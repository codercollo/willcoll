package data

import (
	"context"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/google/uuid"
)

// TestCreateIntentReusesPendingIntentOnRetry covers the ambiguous-PayHero-
// timeout retry path: a tenant resubmitting the same payment while their
// first intent is still pending and unexpired must get back the same
// intent and external_reference, not a fresh one — otherwise a second STK
// prompt to the same phone risks a double charge.
func TestCreateIntentReusesPendingIntentOnRetry(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	channel := "PH-CH-RETRY"
	property := &Property{
		LandlordID: landlord, Name: "Retry Pay Property", Location: "Nairobi",
		Slug: "retry-pay-" + uuid.NewString()[:8], WaterRatePerUnit: biMoney(t, "150"),
		PayheroChannelID: &channel,
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "R1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "RETRY TENANT", PrimaryPhone: "+254700000098",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: LeaseStatusActive}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	target := PayTarget{TenantID: tenant, PropertyID: property.ID, UnitID: unit.ID}
	lines := []IntentLine{{Type: LedgerTypeRent, Amount: biMoney(t, "5000")}}

	first, err := models.PayAccess.CreateIntent(ctx, target, "+254700000001", lines, time.Minute)
	if err != nil {
		t.Fatalf("first CreateIntent: %v", err)
	}

	// Simulating the tenant resubmitting after an ambiguous timeout: the
	// handler never marks the first intent failed, so this must resolve to
	// the very same intent and reference, not a new one.
	second, err := models.PayAccess.CreateIntent(ctx, target, "+254700000001", lines, time.Minute)
	if err != nil {
		t.Fatalf("second CreateIntent: %v", err)
	}
	if second.ID != first.ID || second.Reference != first.Reference {
		t.Fatalf("retry minted a new intent: first = %+v, second = %+v", first, second)
	}

	// Once the first intent is no longer pending (failed, expired, or
	// completed), a resubmit must mint a genuinely new one.
	if err := models.PayAccess.FailIntent(ctx, tenant, first.ID, "test: force a fresh retry"); err != nil {
		t.Fatalf("FailIntent: %v", err)
	}
	third, err := models.PayAccess.CreateIntent(ctx, target, "+254700000001", lines, time.Minute)
	if err != nil {
		t.Fatalf("third CreateIntent: %v", err)
	}
	if third.ID == first.ID || third.Reference == first.Reference {
		t.Fatalf("expected a fresh intent once the first was failed, got the same one: %+v", third)
	}
}
