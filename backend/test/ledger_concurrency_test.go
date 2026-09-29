// Package test holds cross-package integration tests that need a real
// PostgreSQL with every migration applied. They skip themselves unless
// WILLCOLL_TEST_DB_DSN (the willcoll_app role) and WILLCOLL_TEST_ADMIN_DB_DSN
// (the willcoll_admin role) are set; scripts/test_integration.sh builds a
// throwaway database and sets both.
package test

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// env connects the two pools, or skips.
func env(t *testing.T) (models data.Models, app *sql.DB) {
	t.Helper()
	appDSN, adminDSN := os.Getenv("WILLCOLL_TEST_DB_DSN"), os.Getenv("WILLCOLL_TEST_ADMIN_DB_DSN")
	if appDSN == "" || adminDSN == "" {
		t.Skip("WILLCOLL_TEST_DB_DSN and WILLCOLL_TEST_ADMIN_DB_DSN not set; skipping")
	}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(40)
		t.Cleanup(func() { db.Close() })
		if err := db.Ping(); err != nil {
			t.Fatal(err)
		}
		return db
	}
	app = open(appDSN)
	return data.NewModels(app, open(adminDSN), 30*time.Second), app
}

func money(t *testing.T, s string) moneyfmt.Money {
	t.Helper()
	m, err := moneyfmt.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// tenantTx runs fn as the tenant, on the RLS-subject API role.
func tenantTx(t *testing.T, db *sql.DB, tenant uuid.UUID, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
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

type world struct {
	models  data.Models
	db      *sql.DB
	tenant  uuid.UUID
	channel string
	unit    uuid.UUID
}

// newWorld seeds a manager with one property (on its own PayHero channel), one
// unit, and a tenant on it.
func newWorld(t *testing.T) *world {
	t.Helper()
	models, db := env(t)
	ctx := context.Background()

	w := &world{models: models, db: db, tenant: uuid.New(), channel: "CH-" + uuid.NewString()[:8]}
	if _, err := db.Exec(`INSERT INTO managers (id, firm_name, username, email, phone, password_hash)
		VALUES ($1, 'Concurrency Test', $2, $3, '+254700000000', '\x00')`, w.tenant, "u-"+w.tenant.String(), w.tenant.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM managers WHERE id = $1`, w.tenant) })

	landlord := uuid.New()
	tenantTx(t, db, w.tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, w.tenant); err != nil {
			t.Fatal(err)
		}
	})

	property := &data.Property{LandlordID: landlord, Name: "Race Court", Location: "Nairobi", Slug: "race-" + uuid.NewString()[:8], PayheroChannelID: &w.channel}
	if err := models.Properties.Insert(ctx, w.tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &data.Unit{PropertyID: property.ID, UnitCode: "R1", Status: "vacant"}
	if err := models.Units.Insert(ctx, w.tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &data.Lease{UnitID: unit.ID, TenantName: "RACE TENANT", PrimaryPhone: "+254722555001",
		RentAmount: money(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, w.tenant, w.tenant, lease); err != nil {
		t.Fatal(err)
	}
	w.unit = unit.ID
	return w
}

func (w *world) owe(t *testing.T, ledger, amount string) {
	t.Helper()
	if err := w.models.Rent.PostManualCharge(context.Background(), w.tenant, w.tenant, w.unit, ledger, money(t, amount), "seed"); err != nil {
		t.Fatal(err)
	}
}

func (w *world) balance(t *testing.T, ledger string) moneyfmt.Money {
	t.Helper()
	var b moneyfmt.Money
	tenantTx(t, w.db, w.tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT COALESCE((SELECT balance FROM unit_ledger_balances WHERE unit_id = $1 AND type = $2), 0)`, w.unit, ledger).Scan(&b); err != nil {
			t.Fatal(err)
		}
	})
	return b
}

func (w *world) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	tenantTx(t, w.db, w.tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
	})
	return n
}

func (w *world) payload(receipt, amount string) *payhero.CollectionsWebhookPayload {
	return &payhero.CollectionsWebhookPayload{
		Success: true, MpesaReceipt: receipt, Amount: amount, Msisdn: "254722555001",
		AccountReference: "BANK-ACC", ChannelID: w.channel, Raw: []byte(`{}`),
	}
}

// fireTogether runs fn(i) on n goroutines released at the same instant.
func fireTogether(n int, fn func(i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			fn(i)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

// assertZeroSum checks the ledger invariants for every payment of the tenant:
// each fully allocated payment is backed by allocations adding up to exactly
// its amount, each allocation by one CREDIT ledger entry of the same amount,
// and total credits posted equal total money allocated.
func (w *world) assertZeroSum(t *testing.T) {
	t.Helper()
	tenantTx(t, w.db, w.tenant, func(tx *sql.Tx) {
		var bad int
		if err := tx.QueryRow(`
			SELECT count(*) FROM payments p
			WHERE p.status = 'allocated'
			  AND p.amount <> COALESCE((SELECT SUM(amount) FROM payment_allocations WHERE payment_id = p.id), 0)`).Scan(&bad); err != nil {
			t.Fatal(err)
		}
		if bad != 0 {
			t.Errorf("%d allocated payments whose allocations do not add up to the payment", bad)
		}

		if err := tx.QueryRow(`
			SELECT count(*) FROM payment_allocations pa
			LEFT JOIN ledger_entries le ON le.id = pa.ledger_entry_id
			WHERE le.id IS NULL OR le.direction <> 'CREDIT' OR le.amount <> pa.amount`).Scan(&bad); err != nil {
			t.Fatal(err)
		}
		if bad != 0 {
			t.Errorf("%d allocations without a matching CREDIT entry", bad)
		}

		var credits, allocated moneyfmt.Money
		if err := tx.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE direction = 'CREDIT' AND reference_type = 'payment_allocation'`).Scan(&credits); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM payment_allocations`).Scan(&allocated); err != nil {
			t.Fatal(err)
		}
		if credits != allocated {
			t.Errorf("CREDIT entries total %s but allocations total %s", credits, allocated)
		}
	})
}

// TestDuplicateWebhooksPostOnce fires the same PayHero delivery at the
// pipeline from many goroutines at once, the way PayHero retries do. The
// receipt must be recorded once, allocated once, credited once.
func TestDuplicateWebhooksPostOnce(t *testing.T) {
	w := newWorld(t)
	w.owe(t, data.LedgerTypeRent, "5000")

	const deliveries = 12
	results := make([]*data.IngestResult, deliveries)
	errs := make([]error, deliveries)
	payload := w.payload("RACE-DUP-1", "5000.00")

	fireTogether(deliveries, func(i int) {
		results[i], errs[i] = w.models.ProcessCollectionsPayment(context.Background(), payload)
	})

	fresh := 0
	for i := range results {
		if errs[i] != nil {
			t.Errorf("delivery %d failed: %v", i, errs[i])
			continue
		}
		if !results[i].Duplicate {
			fresh++
		}
	}
	if fresh != 1 {
		t.Errorf("%d deliveries were processed as new, want exactly 1", fresh)
	}
	if n := w.count(t, `SELECT count(*) FROM payments WHERE mpesa_receipt = $1`, "RACE-DUP-1"); n != 1 {
		t.Errorf("payments rows for the receipt = %d, want exactly 1", n)
	}
	if n := w.count(t, `SELECT count(*) FROM payment_allocations pa JOIN payments p ON p.id = pa.payment_id WHERE p.mpesa_receipt = $1`, "RACE-DUP-1"); n != 1 {
		t.Errorf("allocations = %d, want 1", n)
	}
	if b := w.balance(t, data.LedgerTypeRent); !b.IsZero() {
		t.Errorf("rent balance = %s, want 0.00 (credited once, not %d times)", b, deliveries)
	}
	w.assertZeroSum(t)
}

// TestConcurrentDistinctPaymentsAllPost sends many different payments for one
// unit at once. Serializable transactions with retry must apply every one:
// none lost, none doubled.
func TestConcurrentDistinctPaymentsAllPost(t *testing.T) {
	w := newWorld(t)
	const payments = 10
	w.owe(t, data.LedgerTypeRent, "1000") // ten payments of 100

	errs := make([]error, payments)
	fireTogether(payments, func(i int) {
		_, errs[i] = w.models.ProcessCollectionsPayment(context.Background(), w.payload("RACE-D-"+uuid.NewString()[:8], "100.00"))
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("payment %d failed: %v", i, err)
		}
	}
	if b := w.balance(t, data.LedgerTypeRent); !b.IsZero() {
		t.Errorf("rent balance = %s, want 0.00: some payment was lost or applied twice", b)
	}
	// Each payment is placed (by exact combination or, when no combination of
	// the shrinking balance fits, the default waterfall): none left unplaced.
	if n := w.count(t, `SELECT count(*) FROM payments p WHERE EXISTS (SELECT 1 FROM payment_allocations WHERE payment_id = p.id)`); n != payments {
		t.Errorf("payments with allocations = %d, want %d", n, payments)
	}
	w.assertZeroSum(t)
}

// TestConcurrentPaymentAndReversal races a payment against a manager
// reversing an entry. Whatever the interleaving, the ledger must still add up.
func TestConcurrentPaymentAndReversal(t *testing.T) {
	w := newWorld(t)
	w.owe(t, data.LedgerTypeRent, "3000")
	if _, err := w.models.ProcessCollectionsPayment(context.Background(), w.payload("RACE-R-1", "3000.00")); err != nil {
		t.Fatal(err)
	}

	var entry uuid.UUID
	tenantTx(t, w.db, w.tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT le.id FROM ledger_entries le JOIN payment_allocations pa ON pa.ledger_entry_id = le.id LIMIT 1`).Scan(&entry); err != nil {
			t.Fatal(err)
		}
	})

	reversals := make([]error, 4)
	var other error
	fireTogether(5, func(i int) {
		if i == 4 {
			_, other = w.models.ProcessCollectionsPayment(context.Background(), w.payload("RACE-R-2", "500.00"))
			return
		}
		_, reversals[i] = w.models.Rent.ReverseEntry(context.Background(), w.tenant, w.tenant, entry, "race")
	})

	won := 0
	for _, err := range reversals {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Errorf("%d concurrent reversals succeeded, want exactly 1", won)
	}
	if other != nil {
		t.Errorf("the unrelated payment failed: %v", other)
	}
	// 3,000 owed, 3,000 paid then reversed (owed again), 500 paid: 2,500 left.
	if b := w.balance(t, data.LedgerTypeRent); b != money(t, "2500") {
		t.Errorf("rent balance = %s, want 2500.00", b)
	}
	w.assertZeroSum(t)
}
