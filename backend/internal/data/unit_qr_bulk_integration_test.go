package data

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestBulkGetOrCreate covers the 2026-09-28 Feature 1 bulk sticker run: it
// creates a code for a lease with none, reuses one that already has a live
// code (never rotating it), skips a lease on a different property, skips a
// lease id that doesn't exist, and returns the stickers naturally sorted by
// unit (A2, A10, B1 — not the ASCII order A10, A2, B1).
func TestBulkGetOrCreate(t *testing.T) {
	models, conn, _ := biModels(t)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{LandlordID: landlord, Name: "Bulk QR Property", Location: "Nairobi", Slug: "bulk-qr-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	other := &Property{LandlordID: landlord, Name: "Other Property", Location: "Nairobi", Slug: "bulk-qr-other-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, other); err != nil {
		t.Fatal(err)
	}

	mkLease := func(p *Property, code string) *Lease {
		unit := &Unit{PropertyID: p.ID, UnitCode: code, Status: "vacant"}
		if err := models.Units.Insert(ctx, tenant, unit); err != nil {
			t.Fatal(err)
		}
		lease := &Lease{UnitID: unit.ID, TenantName: "TENANT " + code, PrimaryPhone: "+254700000" + code, RentAmount: biMoney(t, "5000"),
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
		if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
			t.Fatal(err)
		}
		return lease
	}

	leaseA2 := mkLease(property, "A2")
	leaseA10 := mkLease(property, "A10")
	leaseB1 := mkLease(property, "B1")
	leaseOnOtherProperty := mkLease(other, "X1")

	// A2 already has a live code: the bulk run must reuse it, not rotate.
	existing, err := models.UnitQR.GetOrCreate(ctx, tenant, tenant, leaseA2.ID)
	if err != nil {
		t.Fatalf("seed GetOrCreate: %v", err)
	}

	missingLeaseID := uuid.New()
	result, err := models.UnitQR.BulkGetOrCreate(ctx, tenant, tenant, property.ID,
		[]uuid.UUID{leaseA2.ID, leaseA10.ID, leaseB1.ID, leaseOnOtherProperty.ID, missingLeaseID})
	if err != nil {
		t.Fatalf("BulkGetOrCreate: %v", err)
	}

	if result.Created != 2 {
		t.Errorf("created = %d, want 2 (A10, B1)", result.Created)
	}
	if result.Reused != 1 {
		t.Errorf("reused = %d, want 1 (A2)", result.Reused)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want 2 entries", result.Skipped)
	}
	skippedReasons := map[uuid.UUID]string{}
	for _, s := range result.Skipped {
		skippedReasons[s.LeaseID] = s.Reason
	}
	if skippedReasons[leaseOnOtherProperty.ID] != "not a unit on this property" {
		t.Errorf("other-property lease reason = %q", skippedReasons[leaseOnOtherProperty.ID])
	}
	if skippedReasons[missingLeaseID] != "lease not found" {
		t.Errorf("missing lease reason = %q", skippedReasons[missingLeaseID])
	}

	if len(result.Stickers) != 3 {
		t.Fatalf("stickers = %d, want 3", len(result.Stickers))
	}
	gotOrder := make([]string, len(result.Stickers))
	for i, s := range result.Stickers {
		gotOrder[i] = s.UnitLabel
	}
	if want := []string{"A2", "A10", "B1"}; gotOrder[0] != want[0] || gotOrder[1] != want[1] || gotOrder[2] != want[2] {
		t.Errorf("sticker order = %v, want %v (natural sort)", gotOrder, want)
	}

	for _, s := range result.Stickers {
		if s.PropertyName != property.Name || s.PropertySlug != property.Slug {
			t.Errorf("sticker %+v has the wrong property context", s)
		}
		if s.UnitLabel == "A2" && s.QR.Token != existing.Token {
			t.Errorf("A2's code was rotated: got token %q, want the existing %q", s.QR.Token, existing.Token)
		}
	}
}
