package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/google/uuid"
)

// Property scope at the data layer (docs/security/authorization.md D2,
// migration 000047): a context carrying auth.WithPropertyScope makes the
// database itself hide every row outside the allowed properties, whichever
// model or query asks.

type scopeWorld struct {
	conn   *sql.DB
	models Models
	tenant uuid.UUID

	landlordA, landlordB uuid.UUID
	propA, propB         *Property
	unitA, unitB         *Unit
	leaseA, leaseB       *Lease
	unmatched            *Payment

	scoped context.Context // restricted to property A
	full   context.Context // unrestricted
}

func newScopeWorld(t *testing.T) *scopeWorld {
	t.Helper()
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), 0)
	ctx := context.Background()

	w := &scopeWorld{conn: conn, models: models}
	var landlordA uuid.UUID
	w.tenant, landlordA = seedTenant(t, conn)
	w.landlordA = landlordA
	lb := &Landlord{Name: "Landlord B", Phone: "+254722222222"}
	if err := models.Landlords.Insert(ctx, w.tenant, lb); err != nil {
		t.Fatal(err)
	}
	w.landlordB = lb.ID

	mkProperty := func(name string, landlord uuid.UUID) *Property {
		p := &Property{LandlordID: landlord, Name: name, Location: "Nairobi", Slug: "ps-" + uuid.NewString()[:8],
			GarbageEnabled: true, GarbageFee: biMoney(t, "300"), WaterRatePerUnit: biMoney(t, "150"), ManagementFeePercent: 5,
			ElectricityEnabled: true, ElectricityDepositAmount: biMoney(t, "1000")}
		if err := models.Properties.Insert(ctx, w.tenant, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	w.propA, w.propB = mkProperty("Property A", landlordA), mkProperty("Property B", w.landlordB)

	period := biPeriod(t, "2026-08")
	seedUnit := func(p *Property, code string) (*Unit, *Lease) {
		u := &Unit{PropertyID: p.ID, UnitCode: code, Status: "vacant"}
		if err := models.Units.Insert(ctx, w.tenant, u); err != nil {
			t.Fatal(err)
		}
		l := &Lease{UnitID: u.ID, TenantName: "TENANT " + code, PrimaryPhone: "+254700000000",
			RentAmount: biMoney(t, "10000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Status: "active", GarbageBilled: true, Payers: []LeasePayer{{Name: "PAYER " + code}}}
		if err := models.Leases.Insert(ctx, w.tenant, w.tenant, l); err != nil {
			t.Fatal(err)
		}
		rcCharge(t, conn, w.tenant, u.ID, biMoney(t, "10000"), period.FirstDay(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
		rcPay(t, conn, w.tenant, u.ID, biMoney(t, "4000"), time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
		// Receipts are issued when a payment is posted.
		if _, err := models.Rent.PostPayment(ctx, w.tenant, w.tenant, u.ID, PaymentInput{Amount: biMoney(t, "500"), Source: "manual", Reference: "SEED-" + code}); err != nil {
			t.Fatal(err)
		}
		if err := models.Water.SaveReadings(ctx, w.tenant, w.tenant, p.ID, period, []WaterReadingInput{
			{UnitID: u.ID, CurrentReading: biMoney(t, "10")}}); err != nil {
			t.Fatal(err)
		}
		if _, err := models.UnitQR.GetOrCreate(ctx, w.tenant, w.tenant, l.ID); err != nil {
			t.Fatal(err)
		}
		if err := models.Payments.InsertIntent(ctx, &PaymentIntent{TenantID: w.tenant, UnitID: u.ID, Lines: []byte(`[]`),
			ExternalReference: "ref-" + uuid.NewString(), Phone: "+254700000000", Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		return u, l
	}
	w.unitA, w.leaseA = seedUnit(w.propA, "A1")
	w.unitB, w.leaseB = seedUnit(w.propB, "B1")
	for _, p := range []*Property{w.propA, w.propB} {
		if _, err := models.Water.Generate(ctx, w.tenant, w.tenant, p.ID, period); err != nil {
			t.Fatal(err)
		}
		if _, err := models.Garbage.Generate(ctx, w.tenant, w.tenant, p.ID, period); err != nil {
			t.Fatal(err)
		}
		if _, err := models.Rent.GenerateForPeriod(ctx, w.tenant, w.tenant, p.ID, biPeriod(t, "2026-09")); err != nil {
			t.Fatal(err)
		}
		if _, err := models.Reports.Generate(ctx, w.tenant, w.tenant, p.ID, period); err != nil {
			t.Fatalf("report: %v", err)
		}
		if err := models.Reports.SetPlotMeterReading(ctx, w.tenant, w.tenant, p.ID, period, biMoney(t, "100"), nil, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []*Unit{w.unitA, w.unitB} {
		if err := models.Reports.CreateArrearsCommitment(ctx, w.tenant, w.tenant, u.ID, period, "will pay"); err != nil {
			t.Fatal(err)
		}
	}
	// A payment the engine could not place: it belongs to no unit.
	w.unmatched = &Payment{Source: "payhero_c2b", MpesaReceipt: "UN" + uuid.NewString()[:8], Amount: biMoney(t, "777"),
		Msisdn: "+254733333333", Status: PaymentStatusUnmatched, RawPayload: []byte(`{}`), ReceivedAt: time.Now(), TenantID: w.tenant}
	if _, err := models.Payments.Insert(ctx, w.unmatched); err != nil {
		t.Fatal(err)
	}

	w.full = ctx
	w.scoped = auth.WithPropertyScope(ctx, []uuid.UUID{w.propA.ID})
	return w
}

// A scoped caller sees only their property through the models, and anything
// else is "not found" - the same as a row that does not exist.
func TestPropertyScope_ModelsHideOtherProperties(t *testing.T) {
	w := newScopeWorld(t)
	m, tenant := w.models, w.tenant

	t.Run("properties", func(t *testing.T) {
		list, _, err := m.Properties.GetAll(w.scoped, tenant, nil, Filters{Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].ID != w.propA.ID {
			t.Errorf("scoped list = %d properties, want only A", len(list))
		}
		if _, err := m.Properties.Get(w.scoped, tenant, w.propB.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("Get(B) = %v, want not found", err)
		}
		if _, err := m.Properties.Get(w.scoped, tenant, w.propA.ID); err != nil {
			t.Errorf("Get(A): %v", err)
		}
		// Control: unrestricted sees both.
		all, _, _ := m.Properties.GetAll(w.full, tenant, nil, Filters{Page: 1, PageSize: 50})
		if len(all) != 2 {
			t.Errorf("unrestricted list = %d, want 2", len(all))
		}
	})

	t.Run("landlords follow visible properties", func(t *testing.T) {
		list, _, err := m.Landlords.GetAll(w.scoped, tenant, Filters{Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].ID != w.landlordA {
			t.Errorf("scoped landlords = %d, want only A's", len(list))
		}
		if _, err := m.Landlords.Get(w.scoped, tenant, w.landlordB); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("Get(landlord B) = %v, want not found", err)
		}
	})

	t.Run("units, leases, ledger", func(t *testing.T) {
		units, _, err := m.Units.GetAllForProperty(w.scoped, tenant, w.propB.ID, UnitListFilter{}, Filters{Page: 1, PageSize: 50})
		if len(units) != 0 || (err != nil && !errors.Is(err, ErrPropertyNotFound)) {
			t.Errorf("units of B = %d (err %v), want none or property not found", len(units), err)
		}
		if _, err := m.Units.Get(w.scoped, tenant, w.unitB.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("Units.Get(B1) = %v, want not found", err)
		}
		if _, err := m.Leases.Get(w.scoped, tenant, w.leaseB.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("Leases.Get(B) = %v, want not found", err)
		}
		if ls, err := m.Leases.ListForUnit(w.scoped, tenant, w.unitB.ID); len(ls) != 0 || (err != nil && !errors.Is(err, ErrRecordNotFound)) {
			t.Errorf("ListForUnit(B1) = %d (err %v), want none or not found", len(ls), err)
		}
		if _, err := m.Ledger.GetUnitLedger(w.scoped, tenant, w.unitB.ID, LedgerTypeRent, Filters{Page: 1, PageSize: 50}); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("Ledger(B1) = %v, want not found", err)
		}
		if _, err := m.UnitQR.GetOrCreate(w.scoped, tenant, tenant, w.leaseB.ID); err == nil {
			t.Error("issued a QR code for a lease outside the scope")
		}
		if _, err := m.Ledger.GetUnitLedger(w.scoped, tenant, w.unitA.ID, LedgerTypeRent, Filters{Page: 1, PageSize: 50}); err != nil {
			t.Errorf("Ledger(A1): %v", err)
		}
	})

	t.Run("dashboards and reports", func(t *testing.T) {
		rows, err := m.Rent.GetPropertyOverview(w.scoped, tenant, w.propB.ID, biPeriod(t, "2026-08"))
		if len(rows) != 0 || (err != nil && !errors.Is(err, ErrRecordNotFound)) {
			t.Errorf("overview of B = %d rows (err %v), want none or not found", len(rows), err)
		}
		rows, err = m.Rent.GetPropertyOverview(w.scoped, tenant, w.propA.ID, biPeriod(t, "2026-08"))
		if err != nil || len(rows) != 1 {
			t.Errorf("overview of A = %d rows (err %v), want 1", len(rows), err)
		}
		if dep, err := m.Electricity.Deposits(w.scoped, tenant, w.propB.ID); err == nil && dep != nil && len(dep.Rows) != 0 {
			t.Errorf("deposits of B leak %d rows", len(dep.Rows))
		}
		if _, err := m.Reports.Preview(w.scoped, tenant, w.propB.ID, biPeriod(t, "2026-08")); err == nil {
			t.Error("report preview of B succeeded")
		}
	})

	t.Run("payment review queue", func(t *testing.T) {
		// Unrestricted sees the unmatched payment; scoped must not (it names no unit).
		for name, ctx := range map[string]context.Context{"full": w.full, "scoped": w.scoped} {
			q, _, err := m.Payments.ListForReview(ctx, tenant, Filters{Page: 1, PageSize: 100})
			if err != nil {
				t.Fatal(err)
			}
			var sawUnmatched bool
			for _, p := range q {
				if p.ID == w.unmatched.ID {
					sawUnmatched = true
				}
				if name == "scoped" && p.MatchedUnitID != nil && *p.MatchedUnitID == w.unitB.ID {
					t.Errorf("scoped review queue shows a payment for B's unit")
				}
			}
			if (name == "full") != sawUnmatched {
				t.Errorf("%s review queue sees the unmatched payment = %v", name, sawUnmatched)
			}
		}
	})
}

// Writes aimed outside the scope are refused, never silently applied.
func TestPropertyScope_WritesOutsideScopeAreRefused(t *testing.T) {
	w := newScopeWorld(t)
	m, tenant := w.models, w.tenant

	err := m.Units.Insert(w.scoped, tenant, &Unit{PropertyID: w.propB.ID, UnitCode: "B-NEW", Status: "vacant"})
	if err == nil {
		t.Fatal("inserted a unit into a property outside the scope")
	}
	if !errors.Is(err, auth.ErrOutOfScope) && !errors.Is(err, ErrRecordNotFound) && !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("Units.Insert into B = %v, want out of scope / not found", err)
	}
	if err := m.Units.Insert(w.scoped, tenant, &Unit{PropertyID: w.propA.ID, UnitCode: "A-NEW", Status: "vacant"}); err != nil {
		t.Errorf("Units.Insert into A: %v", err)
	}

	if _, err := m.Rent.PostPayment(w.scoped, tenant, tenant, w.unitB.ID, PaymentInput{Amount: biMoney(t, "100"), Source: "manual", Reference: "X1"}); err == nil {
		t.Error("recorded a payment on a unit outside the scope")
	}
	if _, err := m.Rent.PostPayment(w.scoped, tenant, tenant, w.unitA.ID, PaymentInput{Amount: biMoney(t, "100"), Source: "manual", Reference: "X2"}); err != nil {
		t.Errorf("payment on A1: %v", err)
	}
	if err := m.Rent.PostManualCharge(w.scoped, tenant, tenant, w.unitB.ID, LedgerTypeRent, biMoney(t, "100"), "x"); err == nil {
		t.Error("posted a charge on a unit outside the scope")
	}
	if err := m.Properties.Update(w.scoped, &Property{ID: w.propB.ID, TenantID: tenant, LandlordID: w.landlordB, Name: "renamed", Location: "x", Slug: w.propB.Slug, Version: w.propB.Version}); err == nil {
		t.Error("updated a property outside the scope")
	}
	// A scoped caller cannot place the unmatched payment (they cannot see it).
	err = m.Payments.MatchAndAllocate(w.scoped, tenant, tenant, w.unmatched.ID, w.unitA.ID, []Allocation{{LedgerType: LedgerTypeRent, Amount: w.unmatched.Amount}})
	if err == nil {
		t.Error("a scoped caller allocated an unmatched payment")
	}
	// The unrestricted caller still can, and cannot be pushed out of scope either.
	if err := m.Payments.MatchAndAllocate(w.full, tenant, tenant, w.unmatched.ID, w.unitB.ID, []Allocation{{LedgerType: LedgerTypeRent, Amount: w.unmatched.Amount}}); err != nil {
		t.Errorf("unrestricted allocate: %v", err)
	}
}

// Another firm's property in the scope list matches nothing: the scope
// narrows the tenant boundary, it never widens it.
func TestPropertyScope_CannotReachAnotherTenant(t *testing.T) {
	w := newScopeWorld(t)
	other := newScopeWorld(t)

	ctx := auth.WithPropertyScope(context.Background(), []uuid.UUID{other.propA.ID})
	if _, err := w.models.Properties.Get(ctx, w.tenant, other.propA.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("tenant 1 reading tenant 2's property with it in scope = %v, want not found", err)
	}
	list, _, err := w.models.Properties.GetAll(ctx, w.tenant, nil, Filters{Page: 1, PageSize: 50})
	if err != nil || len(list) != 0 {
		t.Errorf("tenant 1 with only a foreign property in scope sees %d (err %v), want none", len(list), err)
	}
	// An id that matches nothing at all is a scope that shows nothing, not everything.
	ctx = auth.WithPropertyScope(context.Background(), []uuid.UUID{uuid.New()})
	list, _, err = w.models.Properties.GetAll(ctx, w.tenant, nil, Filters{Page: 1, PageSize: 50})
	if err != nil || len(list) != 0 {
		t.Errorf("a scope of a deleted property sees %d (err %v), want none", len(list), err)
	}
}

// Every table a scoped caller could read is covered, table by table: with
// rows in both properties, the scoped view holds none of B's and some of A's.
func TestPropertyScope_EveryTableFiltered(t *testing.T) {
	w := newScopeWorld(t)

	unitsOf := func(p uuid.UUID) string { return fmt.Sprintf(`(SELECT id FROM units WHERE property_id = '%s')`, p) }
	accountsOf := func(p uuid.UUID) string {
		return fmt.Sprintf(`(SELECT id FROM ledger_accounts WHERE unit_id IN %s)`, unitsOf(p))
	}
	paymentsOf := func(p uuid.UUID) string {
		return fmt.Sprintf(`(SELECT id FROM payments WHERE matched_unit_id IN %s)`, unitsOf(p))
	}
	// table -> predicate selecting the rows that belong to property p.
	belongs := map[string]func(p uuid.UUID) string{
		"properties":              func(p uuid.UUID) string { return fmt.Sprintf(`id = '%s'`, p) },
		"units":                   func(p uuid.UUID) string { return fmt.Sprintf(`property_id = '%s'`, p) },
		"garbage_runs":            func(p uuid.UUID) string { return fmt.Sprintf(`property_id = '%s'`, p) },
		"reports":                 func(p uuid.UUID) string { return fmt.Sprintf(`property_id = '%s'`, p) },
		"property_meter_readings": func(p uuid.UUID) string { return fmt.Sprintf(`property_id = '%s'`, p) },
		"receipt_counters":        func(p uuid.UUID) string { return fmt.Sprintf(`property_id = '%s'`, p) },
		"unit_qr_codes":           func(p uuid.UUID) string { return fmt.Sprintf(`property_id = '%s'`, p) },
		"payment_intents":         func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"leases":                  func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"ledger_accounts":         func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"water_readings":          func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"rent_runs":               func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"receipts":                func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"arrears_commitments":     func(p uuid.UUID) string { return `unit_id IN ` + unitsOf(p) },
		"payments":                func(p uuid.UUID) string { return `matched_unit_id IN ` + unitsOf(p) },
		"lease_payers": func(p uuid.UUID) string {
			return `lease_id IN (SELECT id FROM leases WHERE unit_id IN ` + unitsOf(p) + `)`
		},
		"charges":             func(p uuid.UUID) string { return `ledger_account_id IN ` + accountsOf(p) },
		"ledger_entries":      func(p uuid.UUID) string { return `ledger_account_id IN ` + accountsOf(p) },
		"payment_allocations": func(p uuid.UUID) string { return `payment_id IN ` + paymentsOf(p) },
	}

	count := func(scope []uuid.UUID, table, where string) int {
		var n int
		tx, err := w.conn.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, w.tenant.String()); err != nil {
			t.Fatal(err)
		}
		if len(scope) > 0 {
			if _, err := tx.Exec(`SELECT set_config('app.property_ids', $1, true)`, auth.PropertyScopeSetting(scope)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, table, where)).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		return n
	}

	scope := []uuid.UUID{w.propA.ID}
	var tables []string
	for table := range belongs {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			bB, bA := belongs[table](w.propB.ID), belongs[table](w.propA.ID)
			if n := count(nil, table, bB); n == 0 {
				t.Fatalf("fixture has no %s rows for B; the check below would prove nothing", table)
			}
			if n := count(scope, table, bB); n != 0 {
				t.Errorf("scoped caller sees %d %s rows of property B", n, table)
			}
			if n := count(scope, table, bA); n == 0 {
				t.Errorf("scoped caller sees none of property A's %s rows", table)
			} else if full := count(nil, table, bA); n != full {
				t.Errorf("scoped caller sees %d of A's %d %s rows", n, full, table)
			}
		})
	}

	t.Run("landlords", func(t *testing.T) {
		if n := count(scope, "landlords", fmt.Sprintf(`id = '%s'`, w.landlordB)); n != 0 {
			t.Errorf("scoped caller sees landlord B")
		}
		if n := count(scope, "landlords", fmt.Sprintf(`id = '%s'`, w.landlordA)); n != 1 {
			t.Errorf("scoped caller sees %d rows of landlord A, want 1", n)
		}
	})
	t.Run("unmatched payments are hidden", func(t *testing.T) {
		if n := count(scope, "payments", `matched_unit_id IS NULL`); n != 0 {
			t.Errorf("scoped caller sees %d unmatched payments", n)
		}
		if n := count(nil, "payments", `matched_unit_id IS NULL`); n == 0 {
			t.Error("fixture has no unmatched payment")
		}
	})
}

// A table added later must decide about property scope: it either gets a
// property_scope policy or is named here with the reason it has none.
func TestPropertyScope_EveryTenantTableHasAPolicyOrAnExemption(t *testing.T) {
	conn := openTestDB(t)
	exempt := map[string]string{
		"transaction_headers": "groups ledger entries; carries no unit or tenant data (entries are scoped)",
		"pay_otps":            "used only by the unauthenticated pay surface, which has no staff principal",
	}
	rows, err := conn.Query(`
		SELECT tablename,
		       bool_or(policyname = 'property_scope') AS scoped
		FROM pg_policies
		WHERE schemaname = current_schema()
		GROUP BY tablename
		HAVING bool_or(policyname = 'tenant_isolation')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var table string
		var scoped bool
		if err := rows.Scan(&table, &scoped); err != nil {
			t.Fatal(err)
		}
		seen++
		if _, ok := exempt[table]; !scoped && !ok {
			t.Errorf("table %s has a tenant_isolation policy but no property_scope policy: add one (migration 000047 shows how) or list it as exempt with a reason", table)
		}
		if _, ok := exempt[table]; scoped && ok {
			t.Errorf("table %s is listed as exempt but has a property_scope policy", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen < 20 {
		t.Fatalf("only %d tenant tables found; the query is wrong", seen)
	}
}
