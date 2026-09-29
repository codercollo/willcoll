package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// TestTenantGraphIntegration drives landlords -> properties -> units ->
// leases -> the ledger against real Postgres as willcoll_app, covering
// ownership checks, optimistic locking, and the LEASE_START deposit
// posting (system-design.txt 3.2.1).
func TestTenantGraphIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	tenantA, landlordA := seedTenant(t, conn)
	tenantB, landlordB := seedTenant(t, conn)

	newProperty := func(tenant, landlord uuid.UUID) *Property {
		p := &Property{LandlordID: landlord, Name: "P", Location: "Nairobi", Slug: "p-" + uuid.NewString()[:8]}
		if err := models.Properties.Insert(ctx, tenant, p); err != nil {
			t.Fatalf("Properties.Insert: %v", err)
		}
		return p
	}
	propA := newProperty(tenantA, landlordA)
	propB := newProperty(tenantB, landlordB)

	// --- Landlords: optimistic concurrency.
	landlord, err := models.Landlords.Get(ctx, tenantA, landlordA)
	if err != nil {
		t.Fatalf("Landlords.Get: %v", err)
	}
	stale := *landlord
	landlord.Name = "Renamed"
	if err := models.Landlords.Update(ctx, landlord); err != nil || landlord.Version != 2 {
		t.Fatalf("Landlords.Update: err = %v, version = %d", err, landlord.Version)
	}
	if err := models.Landlords.Update(ctx, &stale); !errors.Is(err, ErrEditConflict) {
		t.Errorf("stale Landlords.Update: err = %v, want ErrEditConflict", err)
	}

	// --- Units: ownership, uniqueness, archived properties.
	unit := &Unit{PropertyID: propA.ID, UnitCode: "3C", Status: UnitStatusVacant}
	if err := models.Units.Insert(ctx, tenantA, unit); err != nil {
		t.Fatalf("Units.Insert: %v", err)
	}
	if err := models.Units.Insert(ctx, tenantA, &Unit{PropertyID: propA.ID, UnitCode: "3C", Status: UnitStatusVacant}); !errors.Is(err, ErrDuplicateUnitCode) {
		t.Errorf("duplicate unit code: err = %v, want ErrDuplicateUnitCode", err)
	}
	if err := models.Units.Insert(ctx, tenantB, &Unit{PropertyID: propA.ID, UnitCode: "X", Status: UnitStatusVacant}); !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("unit on another tenant's property: err = %v, want ErrPropertyNotFound", err)
	}
	unitB := &Unit{PropertyID: propB.ID, UnitCode: "G1", Status: UnitStatusVacant}
	if err := models.Units.Insert(ctx, tenantB, unitB); err != nil {
		t.Fatalf("Units.Insert (B): %v", err)
	}

	staleUnit := *unit
	unit.MeterNumber = ptr("M-001")
	if err := models.Units.Update(ctx, unit); err != nil {
		t.Fatalf("Units.Update: %v", err)
	}
	if err := models.Units.Update(ctx, &staleUnit); !errors.Is(err, ErrEditConflict) {
		t.Errorf("stale Units.Update: err = %v, want ErrEditConflict", err)
	}

	// --- Leases: deposits post to the ledger in the same transaction.
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lease := &Lease{
		UnitID:             unit.ID,
		TenantName:         "Jane Wanjiku",
		PrimaryPhone:       "+254712345678",
		RentAmount:         moneyfmt.FromCents(1_500_000),
		RentDepositAmount:  moneyfmt.FromCents(1_500_000),
		WaterDepositAmount: moneyfmt.FromCents(100_000),
		StartDate:          start,
		Status:             LeaseStatusActive,
	}
	if err := models.Leases.Insert(ctx, tenantA, tenantA, lease); err != nil {
		t.Fatalf("Leases.Insert: %v", err)
	}

	filters := Filters{Page: 1, PageSize: 20}
	wantBalance := map[string]string{
		LedgerTypeRentDeposit:  "15000.00",
		LedgerTypeWaterDeposit: "1000.00",
		LedgerTypeRent:         "0.00",
	}
	for ledgerType, want := range wantBalance {
		ledger, err := models.Ledger.GetUnitLedger(ctx, tenantA, unit.ID, ledgerType, filters)
		if err != nil {
			t.Fatalf("GetUnitLedger(%s): %v", ledgerType, err)
		}
		if got := ledger.Balance.Amount.String(); got != want {
			t.Errorf("%s balance = %s, want %s", ledgerType, got, want)
		}
		wantEntries := 1
		if want == "0.00" {
			wantEntries = 0
		}
		if len(ledger.Entries) != wantEntries {
			t.Errorf("%s has %d entries, want %d", ledgerType, len(ledger.Entries), wantEntries)
		}
		for _, e := range ledger.Entries {
			if e.Direction != DirectionDebit || e.ReferenceType != ReferenceTypeCharge || e.Amount.String() != want {
				t.Errorf("%s entry = %+v", ledgerType, e)
			}
		}
	}

	// A second active lease fails, and rolls back without posting anything.
	second := *lease
	second.ID = uuid.Nil
	if err := models.Leases.Insert(ctx, tenantA, tenantA, &second); !errors.Is(err, ErrDuplicateActiveLease) {
		t.Errorf("second active lease: err = %v, want ErrDuplicateActiveLease", err)
	}
	if ledger, _ := models.Ledger.GetUnitLedger(ctx, tenantA, unit.ID, LedgerTypeRentDeposit, filters); ledger == nil || len(ledger.Entries) != 1 {
		t.Error("failed lease insert left ledger entries behind")
	}

	// Cross-tenant: leases and ledgers on another firm's unit are invisible.
	if err := models.Leases.Insert(ctx, tenantA, tenantA, &Lease{
		UnitID: unitB.ID, TenantName: "X", PrimaryPhone: "+254700000009",
		RentAmount: moneyfmt.FromCents(100), StartDate: start, Status: LeaseStatusActive,
	}); !errors.Is(err, ErrUnitNotFound) {
		t.Errorf("lease on another tenant's unit: err = %v, want ErrUnitNotFound", err)
	}
	if _, err := models.Ledger.GetUnitLedger(ctx, tenantB, unit.ID, LedgerTypeRentDeposit, filters); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("ledger of another tenant's unit: err = %v, want ErrRecordNotFound", err)
	}

	// Termination is an ordinary version-checked update.
	staleLease := *lease
	end := start.AddDate(1, 0, 0)
	lease.Status, lease.EndDate = LeaseStatusTerminated, &end
	if err := models.Leases.Update(ctx, lease); err != nil {
		t.Fatalf("Leases.Update (terminate): %v", err)
	}
	if err := models.Leases.Update(ctx, &staleLease); !errors.Is(err, ErrEditConflict) {
		t.Errorf("stale Leases.Update: err = %v, want ErrEditConflict", err)
	}

	// Archived properties take no new units.
	if err := models.Properties.Delete(ctx, tenantA, propA.ID); err != nil {
		t.Fatalf("Properties.Delete: %v", err)
	}
	if err := models.Units.Insert(ctx, tenantA, &Unit{PropertyID: propA.ID, UnitCode: "9Z", Status: UnitStatusVacant}); !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("unit on archived property: err = %v, want ErrPropertyNotFound", err)
	}
}

func ptr[T any](v T) *T { return &v }
