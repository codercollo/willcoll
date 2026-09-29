package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// TestListingIntegration covers Greenlight ch.9 against real Postgres:
// filters, the tenant-name full-text search, sort order both ways, and
// count(*) OVER() pagination metadata.
func TestListingIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	tenant, landlordA := seedTenant(t, conn)

	landlordB := &Landlord{Name: "Second Landlord", Phone: "+254722000000"}
	if err := models.Landlords.Insert(ctx, tenant, landlordB); err != nil {
		t.Fatalf("Landlords.Insert: %v", err)
	}

	newProperty := func(name string, landlord uuid.UUID) *Property {
		p := &Property{LandlordID: landlord, Name: name, Location: "Nairobi", Slug: "p-" + uuid.NewString()[:8]}
		if err := models.Properties.Insert(ctx, tenant, p); err != nil {
			t.Fatalf("Properties.Insert: %v", err)
		}
		return p
	}
	arcade := newProperty("Runda Arcade", landlordA)
	newProperty("Kiwi Place", landlordA)
	newProperty("Other Estate", landlordB.ID)

	// --- Properties: landlord_id filter.
	props, meta, err := models.Properties.GetAll(ctx, tenant, &landlordB.ID, Filters{Page: 1, PageSize: 20, Sort: "name"})
	if err != nil || len(props) != 1 || props[0].Name != "Other Estate" || meta.TotalRecords != 1 {
		t.Errorf("GetAll(landlord B) = %v, %+v, %v; want only Other Estate", props, meta, err)
	}
	props, meta, err = models.Properties.GetAll(ctx, tenant, &landlordA, Filters{Page: 1, PageSize: 1, Sort: "-name"})
	if err != nil || len(props) != 1 || props[0].Name != "Runda Arcade" || meta.TotalRecords != 2 || meta.LastPage != 2 {
		t.Errorf("GetAll(landlord A, -name, page 1 of 1) = %v, %+v, %v", props, meta, err)
	}

	// --- Units on one property: 3C leased to Jane (co-payer Peter), 3A and G1 vacant.
	newUnit := func(code string) *Unit {
		u := &Unit{PropertyID: arcade.ID, UnitCode: code, Status: UnitStatusVacant}
		if err := models.Units.Insert(ctx, tenant, u); err != nil {
			t.Fatalf("Units.Insert(%s): %v", code, err)
		}
		return u
	}
	leased := newUnit("3C")
	newUnit("3A")
	newUnit("G1")

	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lease := &Lease{
		UnitID: leased.ID, TenantName: "Jane Wanjiku", PrimaryPhone: "+254712345678",
		RentAmount: moneyfmt.FromCents(1_500_000), RentDepositAmount: moneyfmt.FromCents(1_500_000),
		StartDate: start, Status: LeaseStatusActive,
	}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatalf("Leases.Insert: %v", err)
	}
	addLeasePayer(t, conn, tenant, lease.ID, "Peter Otieno")

	// unitStatus reads a unit's status as stored.
	unitStatus := func(id uuid.UUID) string {
		t.Helper()
		u, err := models.Units.Get(ctx, tenant, id)
		if err != nil {
			t.Fatalf("Units.Get: %v", err)
		}
		return u.Status
	}

	// Creating the lease marked the unit occupied, in the same transaction.
	if got := unitStatus(leased.ID); got != UnitStatusOccupied {
		t.Fatalf("unit status after lease start = %s, want occupied", got)
	}

	// A unit edit can't change status: Update ignores it.
	edit, _ := models.Units.Get(ctx, tenant, leased.ID)
	edit.Status = UnitStatusVacant
	edit.MeterNumber = ptr("M-3C")
	if err := models.Units.Update(ctx, edit); err != nil {
		t.Fatalf("Units.Update: %v", err)
	}
	if got := unitStatus(leased.ID); got != UnitStatusOccupied {
		t.Errorf("unit edit changed status to %s; status must follow the lease", got)
	}

	list := func(filter UnitListFilter, filters Filters) ([]*Unit, Metadata) {
		t.Helper()
		units, meta, err := models.Units.GetAllForProperty(ctx, tenant, arcade.ID, filter, filters)
		if err != nil {
			t.Fatalf("GetAllForProperty(%+v): %v", filter, err)
		}
		return units, meta
	}
	byCode := Filters{Page: 1, PageSize: 20, Sort: "unit_code"}

	// Sort by unit_code, and the current lease on each card.
	units, meta := list(UnitListFilter{}, byCode)
	if codes := unitCodes(units); codes != "3A,3C,G1" || meta.TotalRecords != 3 {
		t.Errorf("unit_code order = %s (total %d), want 3A,3C,G1", codes, meta.TotalRecords)
	}
	for _, u := range units {
		switch {
		case u.UnitCode == "3C" && (u.CurrentLease == nil || u.CurrentLease.TenantName != "Jane Wanjiku" || !u.CurrentLease.StartDate.Equal(start)):
			t.Errorf("3C current lease = %+v", u.CurrentLease)
		case u.UnitCode != "3C" && u.CurrentLease != nil:
			t.Errorf("vacant %s has a current lease %+v", u.UnitCode, u.CurrentLease)
		}
	}
	units, _ = list(UnitListFilter{}, Filters{Page: 1, PageSize: 20, Sort: "-unit_code"})
	if codes := unitCodes(units); codes != "G1,3C,3A" {
		t.Errorf("-unit_code order = %s, want G1,3C,3A", codes)
	}

	// Status filter.
	if units, meta := list(UnitListFilter{Status: UnitStatusVacant}, byCode); unitCodes(units) != "3A,G1" || meta.TotalRecords != 2 {
		t.Errorf("vacant = %s (total %d), want 3A,G1", unitCodes(units), meta.TotalRecords)
	}
	if units, _ := list(UnitListFilter{Status: UnitStatusOccupied}, byCode); unitCodes(units) != "3C" {
		t.Errorf("occupied = %s, want 3C", unitCodes(units))
	}

	// Tenant search: tenant name or co-payer, by word prefix, any case.
	for _, search := range []string{"wanj", "JANE", "jane wanjiku", "peter", "otie"} {
		if units, _ := list(UnitListFilter{TenantSearch: search}, byCode); unitCodes(units) != "3C" {
			t.Errorf("search %q = %s, want 3C", search, unitCodes(units))
		}
	}
	for _, search := range []string{"mary", "jane mary", "!!!&|", "' OR 1=1 --"} {
		units, meta := list(UnitListFilter{TenantSearch: search}, byCode)
		if search == "!!!&|" {
			// Nothing searchable: no filter applied.
			if len(units) != 3 {
				t.Errorf("search %q = %s, want all 3 units", search, unitCodes(units))
			}
			continue
		}
		if len(units) != 0 || meta.TotalRecords != 0 {
			t.Errorf("search %q = %s, want none", search, unitCodes(units))
		}
	}

	// Pagination metadata from count(*) OVER().
	if units, meta := list(UnitListFilter{}, Filters{Page: 2, PageSize: 2, Sort: "unit_code"}); unitCodes(units) != "G1" ||
		meta != (Metadata{CurrentPage: 2, PageSize: 2, FirstPage: 1, LastPage: 2, TotalRecords: 3}) {
		t.Errorf("page 2 = %s, %+v", unitCodes(units), meta)
	}

	// --- Ledger sort: a second lease posts a second RENT_DEPOSIT entry.
	end := start.AddDate(0, 6, 0)
	lease.Status, lease.EndDate = LeaseStatusTerminated, &end
	if err := models.Leases.Update(ctx, lease); err != nil {
		t.Fatalf("terminate lease: %v", err)
	}
	if got := unitStatus(leased.ID); got != UnitStatusVacant {
		t.Errorf("unit status after termination = %s, want vacant", got)
	}
	next := &Lease{
		UnitID: leased.ID, TenantName: "Mary Achieng", PrimaryPhone: "+254733000000",
		RentAmount: moneyfmt.FromCents(1_600_000), RentDepositAmount: moneyfmt.FromCents(1_600_000),
		StartDate: end, Status: LeaseStatusActive,
	}
	if err := models.Leases.Insert(ctx, tenant, tenant, next); err != nil {
		t.Fatalf("second Leases.Insert: %v", err)
	}
	if got := unitStatus(leased.ID); got != UnitStatusOccupied {
		t.Errorf("unit status after new lease = %s, want occupied", got)
	}

	// Reactivating the old lease while Mary's is active fails, and leaves
	// the unit occupied.
	lease.Status, lease.EndDate = LeaseStatusActive, nil
	if err := models.Leases.Update(ctx, lease); !errors.Is(err, ErrDuplicateActiveLease) {
		t.Errorf("reactivating onto an occupied unit: err = %v, want ErrDuplicateActiveLease", err)
	}
	if got := unitStatus(leased.ID); got != UnitStatusOccupied {
		t.Errorf("unit status after failed reactivation = %s, want occupied", got)
	}

	for sort, want := range map[string]string{"-created_at": "16000.00,15000.00", "created_at": "15000.00,16000.00"} {
		ledger, err := models.Ledger.GetUnitLedger(ctx, tenant, leased.ID, LedgerTypeRentDeposit, Filters{Page: 1, PageSize: 20, Sort: sort})
		if err != nil {
			t.Fatalf("GetUnitLedger(%s): %v", sort, err)
		}
		got := ""
		for i, e := range ledger.Entries {
			if i > 0 {
				got += ","
			}
			got += e.Amount.String()
		}
		if got != want || ledger.Balance.Amount.String() != "31000.00" {
			t.Errorf("ledger sort %s = %s (balance %s), want %s (balance 31000.00)", sort, got, ledger.Balance.Amount, want)
		}
	}

	// The search follows the active lease: Jane has moved out, Mary is in.
	if units, _ := list(UnitListFilter{TenantSearch: "jane"}, byCode); len(units) != 0 {
		t.Errorf("search jane after move-out = %s, want none", unitCodes(units))
	}
	if units, _ := list(UnitListFilter{TenantSearch: "achieng"}, byCode); unitCodes(units) != "3C" {
		t.Errorf("search achieng = %s, want 3C", unitCodes(units))
	}
}

func unitCodes(units []*Unit) string {
	s := ""
	for i, u := range units {
		if i > 0 {
			s += ","
		}
		s += u.UnitCode
	}
	return s
}

// addLeasePayer inserts a co-payer (the "OR NAME" pattern) directly, as the
// API role inside the tenant's RLS scope; lease_payers has no model yet.
func addLeasePayer(t *testing.T, conn *sql.DB, tenantID, leaseID uuid.UUID, name string) {
	t.Helper()
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lease_payers (tenant_id, lease_id, name) VALUES ($1, $2, $3)`, tenantID, leaseID, name); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
