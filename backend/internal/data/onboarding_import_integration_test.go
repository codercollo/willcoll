package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// TestOnboardingImportIntegration covers bringing an existing building in at
// go-live: units and leases created together, deposits already held recorded
// without pretending a payment, arrears posted as labelled opening debits, and
// the whole file written or refused as one.
func TestOnboardingImportIntegration(t *testing.T) {
	models, conn, _ := biModels(t)
	ctx := context.Background()

	tenant, landlord := seedTenant(t, conn)
	other, _ := seedTenant(t, conn)
	asAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m := func(s string) moneyfmt.Money { return biMoney(t, s) }
	str := func(s string) *string { return &s }

	property := &Property{LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "ob-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	// A1 already exists and is vacant; B1 already has a tenant.
	a1 := &Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	b1 := &Unit{PropertyID: property.ID, UnitCode: "B1", Status: "vacant"}
	for _, u := range []*Unit{a1, b1} {
		if err := models.Units.Insert(ctx, tenant, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := models.Leases.Insert(ctx, tenant, tenant, &Lease{UnitID: b1.ID, TenantName: "EXISTING TENANT", PrimaryPhone: "+254700000009",
		RentAmount: m("5000"), StartDate: asAt.AddDate(-1, 0, 0), Status: "active"}); err != nil {
		t.Fatal(err)
	}

	rows := []OnboardRow{
		{Row: 2, UnitCode: "A1", TenantName: "JOHN KAMAU", Phone: "+254722000001", Rent: m("10000"),
			RentDeposit: m("10000"), WaterDeposit: m("1000"), RentArrears: m("5000"), WaterArrears: m("700")},
		{Row: 3, UnitCode: "C1", MeterNumber: str("W-3"), TenantName: "MARY WANJIKU", Phone: "+254722000002", Rent: m("12000"),
			CoPayerName: str("PETER OMONDI"), CoPayerPhone: str("+254733000001")},
		{Row: 4, UnitCode: "D1", MeterNumber: str("W-4")}, // a vacant unit, no tenant
	}

	count := func(table string) (n int) {
		biTx(t, conn, tenant, func(tx *sql.Tx) {
			if err := tx.QueryRow(`SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
				t.Fatal(err)
			}
		})
		return n
	}
	balance := func(unit uuid.UUID, ledger string) (b string) {
		biTx(t, conn, tenant, func(tx *sql.Tx) {
			if err := tx.QueryRow(`SELECT COALESCE((SELECT balance::text FROM unit_ledger_balances WHERE unit_id = $1 AND type = $2), '0')`, unit, ledger).Scan(&b); err != nil {
				t.Fatal(err)
			}
		})
		return b
	}
	unitsBefore, leasesBefore := count("units"), count("leases")

	// --- A row for a unit that already has a tenant is refused, by row, and
	// nothing else in the file is written either.
	bad := append([]OnboardRow{{Row: 5, UnitCode: "B1", TenantName: "SOMEONE ELSE", Phone: "+254722000003", Rent: m("1000")}}, rows...)
	_, err := models.Leases.ImportOnboarding(ctx, tenant, tenant, property.ID, asAt, bad, false)
	var ie *ImportError
	if !errors.As(err, &ie) || len(ie.Rows) != 1 || ie.Rows[0].Row != 5 || ie.Rows[0].Field != "unit_code" {
		t.Fatalf("a tenant on an occupied unit: err = %v (%+v)", err, ie)
	}
	if count("units") != unitsBefore || count("leases") != leasesBefore {
		t.Fatal("a refused file wrote something")
	}

	// --- Garbage arrears on a property that does not bill garbage.
	_, err = models.Leases.ImportOnboarding(ctx, tenant, tenant, property.ID, asAt, []OnboardRow{
		{Row: 2, UnitCode: "Z9", TenantName: "X", Phone: "+254722000004", Rent: m("1000"), GarbageArrears: m("300")}}, true)
	if !errors.As(err, &ie) || ie.Rows[0].Field != "garbage_arrears" {
		t.Errorf("garbage arrears with garbage off: err = %v", err)
	}

	// --- A dry run reports what would happen and writes nothing.
	dry, err := models.Leases.ImportOnboarding(ctx, tenant, tenant, property.ID, asAt, rows, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.UnitsCreated != 2 || dry.LeasesCreated != 2 || dry.VacantUnits != 1 || dry.MonthlyRent.String() != "22000.00" ||
		dry.RentDepositsHeld.String() != "10000.00" || dry.RentArrears.String() != "5000.00" || dry.WaterArrears.String() != "700.00" {
		t.Errorf("dry run summary = %+v", dry)
	}
	if count("units") != unitsBefore || count("leases") != leasesBefore {
		t.Fatal("a dry run wrote something")
	}

	// --- Another firm cannot import into this property.
	if _, err := models.Leases.ImportOnboarding(ctx, other, other, property.ID, asAt, rows, false); !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("another firm's import: err = %v, want ErrPropertyNotFound", err)
	}

	// --- The real import.
	got, err := models.Leases.ImportOnboarding(ctx, tenant, tenant, property.ID, asAt, rows, false)
	if err != nil || *got != *dry {
		t.Fatalf("import = %+v, %v; want the dry-run summary %+v", got, err, dry)
	}
	if count("units") != unitsBefore+2 || count("leases") != leasesBefore+2 {
		t.Errorf("units %d (want %d), leases %d (want %d)", count("units"), unitsBefore+2, count("leases"), leasesBefore+2)
	}
	var a1Status, a1Lease string
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		_ = tx.QueryRow(`SELECT status FROM units WHERE id = $1`, a1.ID).Scan(&a1Status)
		_ = tx.QueryRow(`SELECT id::text FROM leases WHERE unit_id = $1 AND status = 'active'`, a1.ID).Scan(&a1Lease)
	})
	if a1Status != UnitStatusOccupied {
		t.Errorf("A1 is %s, want occupied", a1Status)
	}

	// Arrears are owed; deposits already held are not.
	for ledger, want := range map[string]string{
		LedgerTypeRent: "5000.00", LedgerTypeWater: "700.00", LedgerTypeRentDeposit: "0.00", LedgerTypeWaterDeposit: "0.00",
	} {
		if got := balance(a1.ID, ledger); got != want {
			t.Errorf("A1 %s balance = %s, want %s", ledger, got, want)
		}
	}

	// The deposit shows as a debit settled by an opening credit: never a payment.
	type entry struct{ direction, refType, txType string }
	var entries []entry
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		rs, err := tx.Query(`SELECT le.direction, le.reference_type, th.type
			FROM ledger_entries le JOIN transaction_headers th ON th.id = le.transaction_header_id
			JOIN ledger_accounts la ON la.id = le.ledger_account_id
			WHERE la.unit_id = $1 AND la.type = 'RENT_DEPOSIT' ORDER BY le.created_at, le.direction DESC`, a1.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		for rs.Next() {
			var e entry
			_ = rs.Scan(&e.direction, &e.refType, &e.txType)
			entries = append(entries, e)
		}
	})
	if len(entries) != 2 || entries[0] != (entry{"DEBIT", "charge", "LEASE_START"}) || entries[1] != (entry{"CREDIT", "opening_balance", "OPENING_BALANCE"}) {
		t.Errorf("A1 rent-deposit entries = %+v", entries)
	}
	if n := count("payments"); n != 0 {
		t.Errorf("%d payments recorded: onboarding must not invent payments", n)
	}
	if n := count("payment_allocations"); n != 0 {
		t.Errorf("%d payment allocations recorded", n)
	}

	// C1 got a lease with a co-payer, no money; D1 is vacant.
	var c1Payers, d1Leases int
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		_ = tx.QueryRow(`SELECT count(*) FROM lease_payers lp JOIN leases l ON l.id = lp.lease_id JOIN units u ON u.id = l.unit_id WHERE u.unit_code = 'C1' AND u.property_id = $1`, property.ID).Scan(&c1Payers)
		_ = tx.QueryRow(`SELECT count(*) FROM leases l JOIN units u ON u.id = l.unit_id WHERE u.unit_code = 'D1' AND u.property_id = $1`, property.ID).Scan(&d1Leases)
	})
	if c1Payers != 1 || d1Leases != 0 {
		t.Errorf("C1 co-payers %d (want 1), D1 leases %d (want 0)", c1Payers, d1Leases)
	}

	// --- Running the same file again is refused: those units now have tenants.
	if _, err := models.Leases.ImportOnboarding(ctx, tenant, tenant, property.ID, asAt, rows[:1], false); !errors.As(err, &ie) {
		t.Errorf("re-importing: err = %v, want an ImportError", err)
	}

	// --- A mistake is corrected the ordinary way: reverse the opening credit and
	// the deposit shows as owing again.
	var creditID uuid.UUID
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT le.id FROM ledger_entries le JOIN ledger_accounts la ON la.id = le.ledger_account_id
			WHERE la.unit_id = $1 AND la.type = 'RENT_DEPOSIT' AND le.reference_type = 'opening_balance'`, a1.ID).Scan(&creditID); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := models.Rent.ReverseEntry(ctx, tenant, tenant, creditID, "Deposit was not actually held"); err != nil {
		t.Fatalf("reversing an opening credit: %v", err)
	}
	if got := balance(a1.ID, LedgerTypeRentDeposit); got != "10000.00" {
		t.Errorf("after reversing the opening credit the deposit balance = %s, want 10000.00", got)
	}

	// --- All or none: a duplicate unit code inside the file aborts everything.
	before := count("units")
	_, err = models.Leases.ImportOnboarding(ctx, tenant, tenant, property.ID, asAt, []OnboardRow{
		{Row: 2, UnitCode: "E1", TenantName: "A", Phone: "+254722000010", Rent: m("1000")},
		{Row: 3, UnitCode: "E1", TenantName: "B", Phone: "+254722000011", Rent: m("1000")},
	}, false)
	if err == nil {
		t.Fatal("a file with a repeated unit code was accepted")
	}
	if count("units") != before {
		t.Errorf("a failed import left %d new units behind", count("units")-before)
	}
	_ = a1Lease
}
