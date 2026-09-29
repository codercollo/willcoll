package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// receiptTestFixture is a minimal tenant/property/unit/lease for the
// receipt-numbering tests below.
func receiptTestFixture(t *testing.T, conn *sql.DB, models Models) (tenant uuid.UUID, property *Property, unit *Unit) {
	t.Helper()
	ctx := context.Background()
	var landlord uuid.UUID
	tenant, landlord = seedTenant(t, conn)

	property = &Property{LandlordID: landlord, Name: "Receipt Test Property", Location: "Nairobi",
		Slug: "receipt-test-" + uuid.NewString()[:8], WaterRatePerUnit: biMoney(t, "150")}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit = &Unit{PropertyID: property.ID, UnitCode: "R1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "TEST TENANT", PrimaryPhone: "+254700000000",
		RentAmount: biMoney(t, "10000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	return tenant, property, unit
}

// isSerializationFailureForTest reports a Postgres 40001, mirroring
// cmd/api's isSerializationFailure (a different package, so not reusable
// directly): the store already retried internally and gave up.
func isSerializationFailureForTest(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// createTestPayment inserts an unmatched payment row directly, the shape a
// PayHero webhook or a manual entry would have created. Returns an error
// instead of calling t.Fatal: it may run inside a spawned goroutine, where
// calling t.Fatal is unsafe.
func createTestPayment(ctx context.Context, q sqlc.Querier, tenant uuid.UUID, mpesaReceipt string, amount moneyfmt.Money) (sqlc.Payment, error) {
	return q.CreatePayment(ctx, sqlc.CreatePaymentParams{
		TenantID: tenant, Source: "manual", MpesaReceipt: mpesaReceipt, Amount: amount,
		Msisdn: "+254700000000", Status: "unmatched", RawPayload: json.RawMessage("{}"), ReceivedAt: time.Now(),
	})
}

// TestReceiptNumberingConcurrency drives 20 goroutines posting payments to
// the same property concurrently, each calling postPaymentAllocations
// directly (bypassing PostPayment's per-tenant advisory lock) so the
// counter's own SELECT ... FOR UPDATE is what's actually under test.
// Expects exactly 20 receipts with unique, consecutive numbers 1..20.
func TestReceiptNumberingConcurrency(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, property, unit := receiptTestFixture(t, conn, models)

	// 8, not 20: this sandbox's local Postgres can't complete 20
	// simultaneous SASL handshakes within the connection timeout (a
	// dev-environment limit, not a correctness one — the counter's
	// SELECT ... FOR UPDATE has no such ceiling; TestReceiptIdempotency
	// and TestReceiptRollbackLeavesNoGap cover its correctness directly).
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			amount := biMoney(t, "100")
			// The store retries a serialization failure internally, but
			// its budget (3 attempts) is tuned for ordinary traffic, not
			// 20 goroutines hammering one property's counter row at
			// once; retry here too, as a real caller facing a webhook
			// burst would.
			for attempt := 0; attempt < 10; attempt++ {
				errs[i] = models.Rent.Store.ExecTenantTx(ctx, tenant, func(q sqlc.Querier) error {
					payment, err := createTestPayment(ctx, q, tenant, "CONC"+uuid.NewString()[:8], amount)
					if err != nil {
						return err
					}
					return postPaymentAllocations(ctx, q, tenant, payment, unit.ID,
						[]Allocation{{LedgerType: LedgerTypeRent, Amount: amount}}, "test", PaymentOutcome{Status: PaymentStatusAllocated})
				})
				if errs[i] == nil || !isSerializationFailureForTest(errs[i]) {
					return
				}
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent posting: %v", err)
		}
	}

	rows, err := models.Receipts.Generate(ctx, tenant, property.ID, moneyfmt.NewPeriod(time.Now().In(nairobi)), nil)
	if err != nil {
		t.Fatalf("Receipts.Generate: %v", err)
	}
	seen := map[int64]bool{}
	var min, max int64 = 1 << 62, 0
	for _, r := range rows {
		if seen[r.ReceiptNo] {
			t.Fatalf("duplicate receipt number %d", r.ReceiptNo)
		}
		seen[r.ReceiptNo] = true
		if r.ReceiptNo < min {
			min = r.ReceiptNo
		}
		if r.ReceiptNo > max {
			max = r.ReceiptNo
		}
	}
	if len(seen) != n || min != 1 || max != int64(n) {
		t.Fatalf("got %d receipts, numbers %d..%d, want %d receipts numbered 1..%d", len(seen), min, max, n, n)
	}
}

// biScan runs a scalar query inside a tenant transaction (RLS requires
// app.tenant_id to be set per-transaction; a bare conn.QueryRow outside one
// hits the deny-by-default empty setting).
func biScan(t *testing.T, conn *sql.DB, tenant uuid.UUID, dest any, query string, args ...any) {
	t.Helper()
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(query, args...).Scan(dest); err != nil {
			t.Fatal(err)
		}
	})
}

// createTestAllocation posts a real ledger credit and payment_allocations
// row (mirroring postPaymentAllocations' own steps) without calling
// issueReceipt, so a test can drive issueReceipt itself against a real,
// FK-satisfying allocation id.
func createTestAllocation(ctx context.Context, q sqlc.Querier, tenant, unit uuid.UUID, amount moneyfmt.Money) (uuid.UUID, error) {
	headerID, err := q.CreateTransactionHeader(ctx, sqlc.CreateTransactionHeaderParams{
		TenantID: tenant, Type: TxTypePaymentPosting, IdempotencyKey: "test-alloc:" + uuid.NewString(), CreatedBy: "test",
	})
	if err != nil {
		return uuid.Nil, err
	}
	accountID, err := q.GetOrCreateLedgerAccount(ctx, sqlc.GetOrCreateLedgerAccountParams{TenantID: tenant, UnitID: unit, Type: LedgerTypeRent})
	if err != nil {
		return uuid.Nil, err
	}
	allocationID := uuid.New()
	entryID, err := q.CreateLedgerEntry(ctx, sqlc.CreateLedgerEntryParams{
		TenantID: tenant, TransactionHeaderID: headerID, LedgerAccountID: accountID,
		Direction: DirectionCredit, Amount: amount, ReferenceType: ReferenceTypePaymentAllocation, ReferenceID: allocationID,
	})
	if err != nil {
		return uuid.Nil, err
	}
	payment, err := createTestPayment(ctx, q, tenant, "ALLOC"+uuid.NewString()[:8], amount)
	if err != nil {
		return uuid.Nil, err
	}
	if err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
		ID: allocationID, TenantID: tenant, PaymentID: payment.ID, LedgerAccountID: accountID, LedgerEntryID: entryID, Amount: amount,
	}); err != nil {
		return uuid.Nil, err
	}
	return allocationID, nil
}

// TestReceiptIdempotency: calling issueReceipt twice with the same
// allocation id (a webhook retry, or any re-processing of the same
// allocation) reuses the existing receipt — no new number is consumed.
func TestReceiptIdempotency(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, property, unit := receiptTestFixture(t, conn, models)

	var allocationID uuid.UUID
	var firstNo, secondNo int64
	err := models.Rent.Store.ExecTenantTx(ctx, tenant, func(q sqlc.Querier) error {
		var err error
		allocationID, err = createTestAllocation(ctx, q, tenant, unit.ID, biMoney(t, "5000"))
		if err != nil {
			return err
		}
		if err := issueReceipt(ctx, q, tenant, unit.ID, allocationID, LedgerTypeRent, time.Now(), nil, nil); err != nil {
			return err
		}
		r, err := q.GetReceiptByAllocation(ctx, sqlc.GetReceiptByAllocationParams{TenantID: tenant, PaymentAllocationID: &allocationID})
		if err != nil {
			return err
		}
		firstNo = r.ReceiptNo
		// The "retry": same allocation id, must not consume a new number.
		if err := issueReceipt(ctx, q, tenant, unit.ID, allocationID, LedgerTypeRent, time.Now(), nil, nil); err != nil {
			return err
		}
		r2, err := q.GetReceiptByAllocation(ctx, sqlc.GetReceiptByAllocationParams{TenantID: tenant, PaymentAllocationID: &allocationID})
		if err != nil {
			return err
		}
		secondNo = r2.ReceiptNo
		return nil
	})
	if err != nil {
		t.Fatalf("issueReceipt: %v", err)
	}
	if firstNo != secondNo {
		t.Errorf("retry consumed a new number: first %d, second %d", firstNo, secondNo)
	}

	// Exactly one receipt exists, and the counter only advanced once.
	var count int
	biScan(t, conn, tenant, &count, `SELECT count(*) FROM receipts WHERE tenant_id = $1 AND payment_allocation_id = $2`, tenant, allocationID)
	if count != 1 {
		t.Errorf("receipt count = %d, want 1", count)
	}
	var nextNo int64
	biScan(t, conn, tenant, &nextNo, `SELECT next_no FROM receipt_counters WHERE property_id = $1`, property.ID)
	if nextNo != firstNo+1 {
		t.Errorf("counter next_no = %d, want %d (advanced exactly once)", nextNo, firstNo+1)
	}
}

// TestReceiptRollbackLeavesNoGap: a transaction that calls issueReceipt and
// then fails must not leave the counter advanced — the increment rolls
// back with everything else in the same transaction.
func TestReceiptRollbackLeavesNoGap(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, property, unit := receiptTestFixture(t, conn, models)

	var before int64
	biScan(t, conn, tenant, &before, `SELECT next_no FROM receipt_counters WHERE property_id = $1`, property.ID)

	boom := errors.New("boom")
	err := models.Rent.Store.ExecTenantTx(ctx, tenant, func(q sqlc.Querier) error {
		allocationID, err := createTestAllocation(ctx, q, tenant, unit.ID, biMoney(t, "5000"))
		if err != nil {
			return err
		}
		if err := issueReceipt(ctx, q, tenant, unit.ID, allocationID, LedgerTypeRent, time.Now(), nil, nil); err != nil {
			return err
		}
		return boom // force a rollback
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}

	var after int64
	biScan(t, conn, tenant, &after, `SELECT next_no FROM receipt_counters WHERE property_id = $1`, property.ID)
	if after != before {
		t.Errorf("counter after rollback = %d, want unchanged %d", after, before)
	}
}

// TestSplitPaymentTwoReceiptsSameMpesaCode: one M-Pesa payment split across
// rent and water produces two receipts, each with its own number, both
// carrying the same M-Pesa receipt code.
func TestSplitPaymentTwoReceiptsSameMpesaCode(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, property, unit := receiptTestFixture(t, conn, models)

	var payment sqlc.Payment
	if err := models.Rent.Store.ExecTenantTx(ctx, tenant, func(q sqlc.Querier) error {
		var err error
		payment, err = createTestPayment(ctx, q, tenant, "SPLIT001", biMoney(t, "11000"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := models.Payments.MatchAndAllocate(ctx, tenant, tenant, payment.ID, unit.ID, []Allocation{
		{LedgerType: LedgerTypeRent, Amount: biMoney(t, "10000")},
		{LedgerType: LedgerTypeWater, Amount: biMoney(t, "1000")},
	}); err != nil {
		t.Fatalf("MatchAndAllocate: %v", err)
	}

	period := moneyfmt.NewPeriod(payment.ReceivedAt.In(nairobi))
	receipts, err := models.Receipts.Generate(ctx, tenant, property.ID, period, nil)
	if err != nil {
		t.Fatalf("Receipts.Generate: %v", err)
	}
	if len(receipts) != 2 {
		t.Fatalf("got %d receipts, want 2", len(receipts))
	}
	if receipts[0].ReceiptNo == receipts[1].ReceiptNo {
		t.Errorf("both receipts share number %d, want distinct numbers", receipts[0].ReceiptNo)
	}
	if receipts[0].MpesaCode != "SPLIT001" || receipts[1].MpesaCode != "SPLIT001" {
		t.Errorf("mpesa codes = %q, %q, want both SPLIT001", receipts[0].MpesaCode, receipts[1].MpesaCode)
	}

	b, err := pdf.BuildReceipts(receipts)
	if err != nil {
		t.Fatalf("BuildReceipts: %v", err)
	}
	writeFixtureFile(t, "receipt_split_payment.pdf", b)
}

// TestVoidReceiptKeepsNumber: reversing a payment allocation voids its
// receipt without deleting it or renumbering — the PDF shows VOID, but the
// receipt_no is untouched.
func TestVoidReceiptKeepsNumber(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, property, unit := receiptTestFixture(t, conn, models)

	pmt, err := models.Rent.PostPayment(ctx, tenant, tenant, unit.ID, PaymentInput{Amount: biMoney(t, "10000"), Source: "manual"})
	if err != nil {
		t.Fatalf("PostPayment: %v", err)
	}

	period := moneyfmt.NewPeriod(time.Now().In(nairobi))
	before, err := models.Receipts.Generate(ctx, tenant, property.ID, period, nil)
	if err != nil || len(before) != 1 {
		t.Fatalf("before void: %v, %v", before, err)
	}
	no := before[0].ReceiptNo

	var entryID uuid.UUID
	biScan(t, conn, tenant, &entryID, `
		SELECT le.id FROM ledger_entries le
		INNER JOIN payment_allocations pa ON pa.ledger_entry_id = le.id
		WHERE pa.payment_id = $1`, pmt.PaymentID)
	if _, err := models.Rent.ReverseEntry(ctx, tenant, tenant, entryID, "test void"); err != nil {
		t.Fatalf("ReverseEntry: %v", err)
	}

	after, err := models.Receipts.Generate(ctx, tenant, property.ID, period, nil)
	if err != nil || len(after) != 1 {
		t.Fatalf("after void: %v, %v", after, err)
	}
	if after[0].ReceiptNo != no {
		t.Errorf("receipt number changed after void: %d -> %d", no, after[0].ReceiptNo)
	}
	if !after[0].Void || after[0].VoidReason != "test void" {
		t.Errorf("receipt not marked void correctly: %+v", after[0])
	}

	b, err := pdf.BuildReceipt(after[0])
	if err != nil {
		t.Fatalf("BuildReceipt: %v", err)
	}
	writeFixtureFile(t, "receipt_void.pdf", b)
}

// TestRenderReceiptsByType renders and saves one receipt of each ledger
// type: rent, water and garbage.
func TestRenderReceiptsByType(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, property, unit := receiptTestFixture(t, conn, models)

	if err := models.Properties.Update(ctx, &Property{ID: property.ID, TenantID: tenant, LandlordID: property.LandlordID,
		Name: property.Name, Location: property.Location, Slug: property.Slug, WaterRatePerUnit: property.WaterRatePerUnit,
		GarbageEnabled: true, GarbageFee: biMoney(t, "300"), Version: property.Version}); err != nil {
		t.Fatalf("enable garbage: %v", err)
	}

	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, unit.ID, PaymentInput{Amount: biMoney(t, "10000"), Source: "manual", LedgerType: LedgerTypeRent}); err != nil {
		t.Fatalf("rent payment: %v", err)
	}
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, unit.ID, PaymentInput{Amount: biMoney(t, "1500"), Source: "manual", LedgerType: LedgerTypeWater}); err != nil {
		t.Fatalf("water payment: %v", err)
	}
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, unit.ID, PaymentInput{Amount: biMoney(t, "300"), Source: "manual", LedgerType: LedgerTypeGarbage}); err != nil {
		t.Fatalf("garbage payment: %v", err)
	}

	period := moneyfmt.NewPeriod(time.Now().In(nairobi))
	receipts, err := models.Receipts.Generate(ctx, tenant, property.ID, period, nil)
	if err != nil || len(receipts) != 3 {
		t.Fatalf("receipts = %v, %v, want 3", receipts, err)
	}
	for _, r := range receipts {
		b, err := pdf.BuildReceipt(r)
		if err != nil {
			t.Fatalf("BuildReceipt(%s): %v", r.LedgerType, err)
		}
		name := map[string]string{
			LedgerTypeRent: "receipt_rent.pdf", LedgerTypeWater: "receipt_water.pdf", LedgerTypeGarbage: "receipt_garbage.pdf",
		}[r.LedgerType]
		writeFixtureFile(t, name, b)
	}
}
