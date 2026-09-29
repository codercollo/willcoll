package data

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

func biMoney(t *testing.T, s string) moneyfmt.Money {
	t.Helper()
	m, err := moneyfmt.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func biPeriod(t *testing.T, s string) moneyfmt.Period {
	t.Helper()
	p, err := moneyfmt.ParsePeriod(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// biTx runs fn in a transaction scoped to the tenant, as the API role.
func biTx(t *testing.T, conn *sql.DB, tenant uuid.UUID, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// biPay records a payment allocated to one ledger account of a unit, the way
// reconciliation will: the payment, a PAYMENT_POSTING header, a CREDIT entry
// and the allocation linking them.
func biPay(t *testing.T, conn *sql.DB, tenant, unit uuid.UUID, ledgerType, amount, source string, receivedAt time.Time) {
	t.Helper()
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		exec := func(q string, args ...any) {
			if _, err := tx.Exec(q, args...); err != nil {
				t.Fatalf("%v\n%s", err, q)
			}
		}
		payment, header, entry, alloc := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		var account uuid.UUID
		if err := tx.QueryRow(`
			INSERT INTO ledger_accounts (tenant_id, unit_id, type) VALUES ($1, $2, $3)
			ON CONFLICT (unit_id, type) DO UPDATE SET type = EXCLUDED.type RETURNING id`,
			tenant, unit, ledgerType).Scan(&account); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO payments (id, tenant_id, source, mpesa_receipt, amount, msisdn, matched_unit_id, status, raw_payload, received_at)
			VALUES ($1, $2, $3, $4, $5, '+254700000000', $6, 'allocated', '{}', $7)`,
			payment, tenant, source, "R"+payment.String()[:9], amount, unit, receivedAt)
		exec(`INSERT INTO transaction_headers (id, tenant_id, type, idempotency_key, created_by)
			VALUES ($1, $2, 'PAYMENT_POSTING', $3, 'test')`, header, tenant, "pay:"+payment.String())
		exec(`INSERT INTO ledger_entries (id, tenant_id, transaction_header_id, ledger_account_id, direction, amount, reference_type, reference_id)
			VALUES ($1, $2, $3, $4, 'CREDIT', $5, 'payment_allocation', $6)`, entry, tenant, header, account, amount, alloc)
		exec(`INSERT INTO payment_allocations (id, tenant_id, payment_id, ledger_account_id, ledger_entry_id, amount)
			VALUES ($1, $2, $3, $4, $5, $6)`, alloc, tenant, payment, account, entry, amount)
	})
}

func biBalance(t *testing.T, conn *sql.DB, tenant, unit uuid.UUID, ledgerType string) moneyfmt.Money {
	t.Helper()
	var b moneyfmt.Money
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT COALESCE((SELECT balance FROM unit_ledger_balances WHERE unit_id = $1 AND type = $2), 0)`,
			unit, ledgerType).Scan(&b); err != nil {
			t.Fatal(err)
		}
	})
	return b
}

// TestBillingIntegration drives water and garbage runs, reports and receipts
// against real Postgres as willcoll_app: ledger postings, double-run
// protection, balances carried forward and stable over time, the Nairobi
// month boundary, receipt numbering, and tenant isolation.
func TestBillingIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	tenant, landlord := seedTenant(t, conn)
	otherTenant, _ := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "ra-" + uuid.NewString()[:8],
		GarbageEnabled: true, GarbageFee: biMoney(t, "300"), WaterRatePerUnit: biMoney(t, "100"), ManagementFeePercent: 10,
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}

	newUnit := func(code string) *Unit {
		u := &Unit{PropertyID: property.ID, UnitCode: code, Status: "vacant"}
		if err := models.Units.Insert(ctx, tenant, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	a1, a2 := newUnit("A1"), newUnit("A2") // A2 stays vacant

	lease := &Lease{UnitID: a1.ID, TenantName: "JOHN KAMAU", PrimaryPhone: "+254722000000",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active", GarbageBilled: true}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO lease_payers (tenant_id, lease_id, name) VALUES ($1, $2, 'MARY WANJIKU')`, tenant, lease.ID); err != nil {
			t.Fatal(err)
		}
	})

	aug, sep, nov := biPeriod(t, "2026-08"), biPeriod(t, "2026-09"), biPeriod(t, "2026-11")

	save := func(p moneyfmt.Period, unit uuid.UUID, current string) error {
		c := biMoney(t, current)
		return models.Water.SaveReadings(ctx, tenant, tenant, property.ID, p, []WaterReadingInput{{UnitID: unit, CurrentReading: c}})
	}

	// --- Water, August: 10 units at 100 = 1,000 owed.
	if err := save(aug, a1.ID, "10"); err != nil {
		t.Fatalf("SaveReadings: %v", err)
	}
	if err := save(aug, a2.ID, "5"); !errors.Is(err, ErrUnitNotInProperty) {
		t.Errorf("reading for a vacant unit: err = %v, want ErrUnitNotInProperty", err)
	}
	if err := models.Water.SaveReadings(ctx, otherTenant, otherTenant, property.ID, aug, nil); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant's property: err = %v, want ErrRecordNotFound", err)
	}

	res, err := models.Water.Generate(ctx, tenant, tenant, property.ID, aug)
	if err != nil || res.Billed != 1 || res.Total != biMoney(t, "1000") {
		t.Fatalf("August water run = %+v, %v", res, err)
	}
	if got := biBalance(t, conn, tenant, a1.ID, LedgerTypeWater); got != biMoney(t, "1000") {
		t.Errorf("water balance after run = %s, want 1000.00", got)
	}

	// A repeat, or a late edit, must not bill twice or change a billed reading.
	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, aug); !errors.Is(err, ErrNothingToGenerate) && !errors.Is(err, ErrRunAlreadyGenerated) {
		t.Errorf("second August run: err = %v", err)
	}
	if err := save(aug, a1.ID, "99"); !errors.Is(err, ErrReadingLocked) {
		t.Errorf("edit of a billed reading: err = %v, want ErrReadingLocked", err)
	}
	if got := biBalance(t, conn, tenant, a1.ID, LedgerTypeWater); got != biMoney(t, "1000") {
		t.Errorf("water balance after repeat = %s, must still be 1000.00", got)
	}

	// --- A part payment, then September: previous reading auto-fills and the
	// bill carries the 600 still owed.
	biPay(t, conn, tenant, a1.ID, LedgerTypeWater, "400", "payhero_stk", time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC))

	grid, err := models.Water.Grid(ctx, tenant, property.ID, sep)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid.Rows) != 1 || grid.Rows[0].UnitCode != "A1" || grid.Rows[0].PreviousReading != biMoney(t, "10") ||
		grid.Rows[0].PriorBalance != biMoney(t, "600") || grid.Rows[0].TenantName != "JOHN KAMAU" {
		t.Fatalf("September grid = %+v", grid.Rows)
	}
	if err := save(sep, a1.ID, "8"); !errors.Is(err, ErrInvalidReading) {
		t.Errorf("current below previous: err = %v, want ErrInvalidReading", err)
	}
	if err := save(sep, a1.ID, "25"); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, sep); err != nil {
		t.Fatalf("September water run: %v", err)
	}

	sepInv, err := models.Water.Invoices(ctx, tenant, property.ID, sep, nil)
	if err != nil || len(sepInv) != 1 {
		t.Fatalf("September invoices = %v, %v", sepInv, err)
	}
	if inv := sepInv[0]; inv.PriorBalance != biMoney(t, "600") || inv.Amount != biMoney(t, "1500") ||
		inv.TotalDue() != biMoney(t, "2100") || inv.CustomerName != "JOHN KAMAU" || inv.PreviousReading != biMoney(t, "10") {
		t.Errorf("September invoice = %+v", inv)
	}

	// The August bill must not change when later charges and payments land.
	biPay(t, conn, tenant, a1.ID, LedgerTypeWater, "700", "manual", time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC))
	augInv, err := models.Water.Invoices(ctx, tenant, property.ID, aug, nil)
	if err != nil || len(augInv) != 1 || augInv[0].PriorBalance != biMoney(t, "0") || augInv[0].TotalDue() != biMoney(t, "1000") {
		t.Errorf("August invoice must stay B/F 0 and due 1000.00: %+v, %v", augInv, err)
	}
	if _, err := models.Water.Invoices(ctx, tenant, property.ID, nov, nil); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("unbilled period: err = %v, want ErrRecordNotFound", err)
	}
	if _, err := models.Water.Invoices(ctx, otherTenant, property.ID, sep, nil); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant's invoices: err = %v, want ErrRecordNotFound", err)
	}

	// --- Garbage, September.
	prev, err := models.Garbage.Preview(ctx, tenant, property.ID, sep)
	if err != nil || !prev.Enabled || prev.AlreadyGenerated || len(prev.Rows) != 1 || prev.Rows[0].TotalDue != biMoney(t, "300") {
		t.Fatalf("garbage preview = %+v, %v", prev, err)
	}
	gres, err := models.Garbage.Generate(ctx, tenant, tenant, property.ID, sep)
	if err != nil || gres.Billed != 1 || gres.Total != biMoney(t, "300") {
		t.Fatalf("garbage run = %+v, %v", gres, err)
	}
	if _, err := models.Garbage.Generate(ctx, tenant, tenant, property.ID, sep); !errors.Is(err, ErrRunAlreadyGenerated) {
		t.Errorf("second garbage run: err = %v, want ErrRunAlreadyGenerated", err)
	}
	if got := biBalance(t, conn, tenant, a1.ID, LedgerTypeGarbage); got != biMoney(t, "300") {
		t.Errorf("garbage balance = %s, want 300.00", got)
	}
	ginv, err := models.Garbage.Invoices(ctx, tenant, property.ID, sep, nil)
	if err != nil || len(ginv) != 1 || ginv[0].Fee != biMoney(t, "300") || ginv[0].PriorBalance != biMoney(t, "0") {
		t.Errorf("garbage invoices = %+v, %v", ginv, err)
	}

	// --- Rent payments in September, one just after midnight October 1st in
	// Nairobi (still Sept 30th in UTC).
	biPay(t, conn, tenant, a1.ID, LedgerTypeRent, "5000", "payhero_stk", time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC))
	biPay(t, conn, tenant, a1.ID, LedgerTypeRent, "2500", "manual", time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC))
	biPay(t, conn, tenant, a1.ID, LedgerTypeRent, "1234", "payhero_stk", time.Date(2026, 9, 30, 21, 30, 0, 0, time.UTC))

	// --- The September schedule.
	rep, err := models.Reports.Generate(ctx, tenant, tenant, property.ID, sep)
	if err != nil {
		t.Fatalf("Reports.Generate: %v", err)
	}
	if len(rep.Rows) != 2 || rep.Rows[0].HouseNo != "A1" {
		t.Fatalf("report rows = %+v", rep.Rows)
	}
	row := rep.Rows[0]
	if len(row.Rent) != 2 || row.Rent[0].Amount != biMoney(t, "5000") || row.Rent[1].Amount != biMoney(t, "2500") {
		t.Errorf("rent cell = %+v, want two stacked payments and none from October 1st (Nairobi)", row.Rent)
	}
	if len(row.Tenants) != 2 || row.Tenants[0] != "JOHN KAMAU" || row.Tenants[1] != "MARY WANJIKU" {
		t.Errorf("tenants = %v", row.Tenants)
	}
	// 5000 + 2500 rent, 400 + 700 water = 8,600 grand total; the
	// management fee is 10% of rent collected only (7,500), not the
	// grand total: 750.
	if rep.Totals.Grand != biMoney(t, "8600") || rep.ManagementFee != biMoney(t, "750") {
		t.Errorf("totals = %+v fee = %s", rep.Totals, rep.ManagementFee)
	}
	// ActualWater is what tenants paid this period (400 Sept 2 + 700 Sept
	// 20 = 1,100), not what was billed (1,500); the -400 deviation is the
	// informational "paid less than billed" note, not a data error.
	if rep.Summary.Occupied != 1 || rep.Summary.Vacant != 1 || rep.Summary.WaterUnits != biMoney(t, "15") ||
		rep.Summary.ActualWater != biMoney(t, "1100") || rep.Deviation != biMoney(t, "-400") {
		t.Errorf("summary = %+v deviation %s", rep.Summary, rep.Deviation)
	}
	if _, err := models.Reports.Preview(ctx, otherTenant, property.ID, sep); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant's report: err = %v, want ErrRecordNotFound", err)
	}

	// Receipts are now issued at posting time, one per allocation — see
	// TestReceiptsIntegration. The payments above went through biPay,
	// a raw-SQL test shortcut that bypasses postPaymentAllocations (and
	// so never calls issueReceipt), which is fine for the ledger-balance
	// assertions this test is about.
	if _, err := models.Receipts.Generate(ctx, tenant, property.ID, nov, nil); !errors.Is(err, ErrNothingToGenerate) {
		t.Errorf("receipts for an unpaid month: err = %v, want ErrNothingToGenerate", err)
	}
	if _, err := models.Receipts.Generate(ctx, otherTenant, property.ID, sep, nil); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant's receipts: err = %v, want ErrRecordNotFound", err)
	}

	// --- Concurrent double click: the November run must bill exactly once.
	if err := save(nov, a1.ID, "35"); err != nil { // 10 units, 1,000
		t.Fatal(err)
	}
	before := biBalance(t, conn, tenant, a1.ID, LedgerTypeWater)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = models.Water.Generate(ctx, tenant, tenant, property.ID, nov)
		}()
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, ErrNothingToGenerate), errors.Is(e, ErrRunAlreadyGenerated):
		default:
			t.Errorf("concurrent run failed unexpectedly: %v", e)
		}
	}
	if ok != 1 {
		t.Errorf("%d concurrent runs succeeded, want exactly 1", ok)
	}
	if got := biBalance(t, conn, tenant, a1.ID, LedgerTypeWater); got != before.Add(biMoney(t, "1000")) {
		t.Errorf("balance after concurrent runs = %s, want %s (billed once)", got, before.Add(biMoney(t, "1000")))
	}
}
