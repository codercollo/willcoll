package data

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type biPaymentRow struct {
	Status      string
	Unconfirmed bool
	Note        string
	Unit        *uuid.UUID
	Intent      *uuid.UUID
	Allocations int
}

func biPaymentByReceipt(t *testing.T, conn *sql.DB, tenant uuid.UUID, receipt string) biPaymentRow {
	t.Helper()
	var r biPaymentRow
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		var note sql.NullString
		var unit, intent uuid.NullUUID
		if err := tx.QueryRow(`SELECT p.status, p.auto_applied_unconfirmed, p.review_note, p.matched_unit_id, p.matched_intent_id,
				(SELECT count(*) FROM payment_allocations WHERE payment_id = p.id)
			FROM payments p WHERE p.mpesa_receipt = $1`, receipt).Scan(&r.Status, &r.Unconfirmed, &note, &unit, &intent, &r.Allocations); err != nil {
			t.Fatalf("payment %s: %v", receipt, err)
		}
		r.Note = note.String
		if unit.Valid {
			r.Unit = &unit.UUID
		}
		if intent.Valid {
			r.Intent = &intent.UUID
		}
	})
	return r
}

// TestCollectionsIntegration drives PayHero payment ingestion against real
// Postgres, resolving the manager across tenants on the admin pool and
// applying the payment as willcoll_app: intent tracks, organic matching by
// phone and name, subset-sum, the waterfall, review flags, redelivery and
// concurrency.
func TestCollectionsIntegration(t *testing.T) {
	conn := openTestDB(t)
	adminDSN := os.Getenv("WILLCOLL_TEST_ADMIN_DB_DSN")
	if adminDSN == "" {
		t.Skip("WILLCOLL_TEST_ADMIN_DB_DSN not set; skipping")
	}
	adminConn, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminConn.Close() })

	models := NewModels(conn, adminConn, integrationTimeout)
	ctx := context.Background()

	tenant, landlord := seedTenant(t, conn)
	channel := "CH-" + uuid.NewString()[:8]
	property := &Property{LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "ra-" + uuid.NewString()[:8],
		PayheroChannelID: &channel}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}

	unit := func(code string) *Unit {
		u := &Unit{PropertyID: property.ID, UnitCode: code, Status: "vacant"}
		if err := models.Units.Insert(ctx, tenant, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	lease := func(u *Unit, name, phone string) *Lease {
		l := &Lease{UnitID: u.ID, TenantName: name, PrimaryPhone: phone, RentAmount: biMoney(t, "5000"),
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
		if err := models.Leases.Insert(ctx, tenant, tenant, l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	a1, a2, a3 := unit("A1"), unit("A2"), unit("A3")
	l1 := lease(a1, "JOHN KAMAU", "+254722000001")
	lease(a2, "ACME TRADERS LTD", "+254722000002")
	lease(a3, "GRACE WAMBUI KARIUKI", "+254722000003")
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO lease_payers (tenant_id, lease_id, name, phone) VALUES ($1, $2, 'MARY WANJIKU', '+254733000001')`, tenant, l1.ID); err != nil {
			t.Fatal(err)
		}
	})

	charge := func(u *Unit, ledger, amount string) {
		if err := models.Rent.PostManualCharge(ctx, tenant, tenant, u.ID, ledger, biMoney(t, amount), "test charge"); err != nil {
			t.Fatal(err)
		}
	}
	bal := func(u *Unit, ledger string) string { return biBalance(t, conn, tenant, u.ID, ledger).String() }

	pay := func(amount, msisdn, payer, ref string) (*IngestResult, string, error) {
		receipt := "RCPT" + uuid.NewString()[:8]
		res, err := models.Collections.Process(ctx, CollectionPayment{
			MpesaReceipt: receipt, Amount: biMoney(t, amount), Msisdn: msisdn, PayerName: payer,
			AccountReference: ref, ChannelID: channel, ReceivedAt: time.Now(),
		})
		return res, receipt, err
	}

	newIntent := func(u *Unit, lines string) string {
		ref := "WC-" + uuid.NewString()[:8]
		in := &PaymentIntent{TenantID: tenant, UnitID: u.ID, Lines: []byte(lines), ExternalReference: ref,
			Phone: "+254722000001", Status: PaymentIntentStatusPending, ExpiresAt: time.Now().Add(time.Hour)}
		if err := models.Payments.InsertIntent(ctx, in); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	intentStatus := func(ref string) string {
		var s string
		biTx(t, conn, tenant, func(tx *sql.Tx) {
			if err := tx.QueryRow(`SELECT status FROM payment_intents WHERE external_reference = $1`, ref).Scan(&s); err != nil {
				t.Fatal(err)
			}
		})
		return s
	}

	// --- Track A: an intent whose lines add up: applied verbatim, intent completed.
	charge(a1, LedgerTypeRent, "5000")
	charge(a1, LedgerTypeWater, "1000")
	ref := newIntent(a1, `[{"type":"RENT","amount":"5000.00"},{"type":"WATER","amount":"1000.00"}]`)
	res, receipt, err := pay("6000", "+254799999999", "SOMEONE ELSE", ref)
	if err != nil || res.Duplicate || res.Status != PaymentStatusAllocated || res.NeedsReview || res.Method != "intent" {
		t.Fatalf("intent payment = %+v, %v", res, err)
	}
	if bal(a1, LedgerTypeRent) != "0.00" || bal(a1, LedgerTypeWater) != "0.00" {
		t.Errorf("A1 balances after intent payment: rent %s water %s", bal(a1, LedgerTypeRent), bal(a1, LedgerTypeWater))
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); row.Status != "allocated" || row.Intent == nil || row.Allocations != 2 || row.Unconfirmed {
		t.Errorf("intent payment row = %+v", row)
	}
	if s := intentStatus(ref); s != PaymentIntentStatusCompleted {
		t.Errorf("intent status = %s, want completed", s)
	}

	// --- Redelivery: the same receipt again changes nothing.
	dup, err := models.Collections.Process(ctx, CollectionPayment{
		MpesaReceipt: receipt, Amount: biMoney(t, "6000"), AccountReference: ref, ChannelID: channel,
	})
	if err != nil || !dup.Duplicate {
		t.Fatalf("redelivery = %+v, %v", dup, err)
	}
	if bal(a1, LedgerTypeRent) != "0.00" {
		t.Errorf("redelivery credited again: rent %s", bal(a1, LedgerTypeRent))
	}

	// --- Track B, phone match, exactly one combination: applied, no review.
	charge(a2, LedgerTypeRent, "8000")
	res, receipt, err = pay("8000", "254722000002", "MPESA NAME", "BANK-ACC-1")
	if err != nil || res.Status != PaymentStatusAllocated || res.NeedsReview || res.Method != "phone" {
		t.Fatalf("phone payment = %+v, %v", res, err)
	}
	if bal(a2, LedgerTypeRent) != "0.00" {
		t.Errorf("A2 rent = %s", bal(a2, LedgerTypeRent))
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); row.Unconfirmed || row.Allocations != 1 {
		t.Errorf("phone payment row = %+v", row)
	}

	// --- No exact combination: default waterfall, flagged unconfirmed. Paid by
	// the co-payer's phone.
	charge(a1, LedgerTypeRent, "4000")
	charge(a1, LedgerTypeWater, "500")
	res, receipt, err = pay("1000", "0733000001", "", "BANK-ACC-1")
	if err != nil || res.Status != PaymentStatusMatched || !res.NeedsReview || res.Method != "phone" {
		t.Fatalf("waterfall payment = %+v, %v", res, err)
	}
	if bal(a1, LedgerTypeWater) != "0.00" || bal(a1, LedgerTypeRent) != "3500.00" {
		t.Errorf("waterfall: water %s (want 0), rent %s (want 3500)", bal(a1, LedgerTypeWater), bal(a1, LedgerTypeRent))
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); !row.Unconfirmed || row.Allocations != 2 || row.Note == "" {
		t.Errorf("waterfall payment row = %+v", row)
	}

	// Confirming the guess settles it: it must leave the review list, not stay
	// there as an already-checked 'matched' payment.
	queue, _, err := models.Payments.ListForReview(ctx, tenant, Filters{Page: 1, PageSize: 50, Sort: "-received_at", SortSafelist: []string{"-received_at"}})
	if err != nil {
		t.Fatalf("list for review: %v", err)
	}
	var guessID uuid.UUID
	for _, rp := range queue {
		if rp.MpesaReceipt == receipt {
			guessID = rp.ID
		}
	}
	if guessID == uuid.Nil {
		t.Fatalf("the waterfall payment %s is not in the review list", receipt)
	}
	if err := models.Payments.Confirm(ctx, tenant, guessID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); row.Unconfirmed || row.Status != PaymentStatusAllocated {
		t.Errorf("after confirm the payment row = %+v, want allocated and confirmed", row)
	}
	queue, _, _ = models.Payments.ListForReview(ctx, tenant, Filters{Page: 1, PageSize: 50, Sort: "-received_at", SortSafelist: []string{"-received_at"}})
	for _, rp := range queue {
		if rp.MpesaReceipt == receipt {
			t.Errorf("a confirmed payment is still in the review list: %+v", rp)
		}
	}

	// --- Ambiguous: the amount fits two combinations, so nothing is applied.
	charge(a2, LedgerTypeRent, "1000")
	charge(a2, LedgerTypeWater, "1000")
	res, receipt, err = pay("1000", "254722000002", "", "BANK-ACC-1")
	if err != nil || res.Status != PaymentStatusUnmatched || !res.NeedsReview || res.Applied {
		t.Fatalf("ambiguous payment = %+v, %v", res, err)
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); row.Status != "unmatched" || row.Allocations != 0 || row.Unit == nil || *row.Unit != a2.ID || row.Note == "" {
		t.Errorf("ambiguous payment row = %+v, want unit known and no allocations", row)
	}
	if bal(a2, LedgerTypeRent) != "1000.00" || bal(a2, LedgerTypeWater) != "1000.00" {
		t.Errorf("ambiguous payment must not touch the ledger: rent %s water %s", bal(a2, LedgerTypeRent), bal(a2, LedgerTypeWater))
	}

	// --- Name-only match: applied, but flagged because the unit was guessed.
	charge(a3, LedgerTypeRent, "7000")
	res, receipt, err = pay("7000", "0799000000", "WAMBUI GRACE KARIUKI", "BANK-ACC-1")
	if err != nil || res.Status != PaymentStatusMatched || !res.NeedsReview || res.Method != "name" {
		t.Fatalf("name payment = %+v, %v", res, err)
	}
	if bal(a3, LedgerTypeRent) != "0.00" {
		t.Errorf("A3 rent = %s", bal(a3, LedgerTypeRent))
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); !row.Unconfirmed || row.Allocations != 1 {
		t.Errorf("name payment row = %+v", row)
	}

	// --- Nobody identified: recorded, unmatched, ledger untouched.
	res, receipt, err = pay("2500", "0799111111", "TOTAL STRANGER", "BANK-ACC-1")
	if err != nil || res.Status != PaymentStatusUnmatched || !res.NeedsReview || res.UnitID != nil {
		t.Fatalf("unmatched payment = %+v, %v", res, err)
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); row.Status != "unmatched" || row.Allocations != 0 || row.Note == "" {
		t.Errorf("unmatched payment row = %+v", row)
	}

	// --- An intent whose lines do not match what was paid: unit known from the
	// intent, applied by balance and flagged.
	charge(a3, LedgerTypeRent, "3000")
	ref = newIntent(a3, `[{"type":"RENT","amount":"3500.00"}]`)
	res, receipt, err = pay("3000", "0799222222", "", ref)
	if err != nil || res.Method != "intent" || res.UnitID == nil || *res.UnitID != a3.ID {
		t.Fatalf("mismatched intent payment = %+v, %v", res, err)
	}
	if row := biPaymentByReceipt(t, conn, tenant, receipt); row.Intent == nil || row.Allocations != 1 {
		t.Errorf("mismatched intent row = %+v", row)
	}
	if s := intentStatus(ref); s != PaymentIntentStatusPending {
		t.Errorf("a mismatched payment must not complete the intent, status = %s", s)
	}

	// --- Overpayment becomes an advance on RENT.
	res, _, err = pay("500", "254722000003", "", "BANK-ACC-1")
	if err != nil || res.Status != PaymentStatusMatched {
		t.Fatalf("advance payment = %+v, %v", res, err)
	}
	if bal(a3, LedgerTypeRent) != "-500.00" {
		t.Errorf("A3 advance = %s, want -500.00", bal(a3, LedgerTypeRent))
	}

	// --- Failed STK push.
	ref = newIntent(a1, `[{"type":"RENT","amount":"100.00"}]`)
	if err := models.Collections.FailIntent(ctx, ref, "cancelled by customer"); err != nil {
		t.Fatal(err)
	}
	if s := intentStatus(ref); s != PaymentIntentStatusFailed {
		t.Errorf("failed intent status = %s", s)
	}
	completedRef := newIntent(a1, `[{"type":"RENT","amount":"100.00"}]`)
	if _, _, err := pay("100", "0722000001", "", completedRef); err != nil {
		t.Fatal(err)
	}
	if err := models.Collections.FailIntent(ctx, completedRef, "late failure"); err != nil {
		t.Fatal(err)
	}
	if s := intentStatus(completedRef); s != PaymentIntentStatusCompleted {
		t.Errorf("a completed intent must not be failed, status = %s", s)
	}
	if err := models.Collections.FailIntent(ctx, "WC-UNKNOWN", "x"); err != nil {
		t.Errorf("unknown reference should be ignored, got %v", err)
	}

	// --- Concurrent redelivery of one receipt credits once.
	charge(a1, LedgerTypeRent, "700")
	before := bal(a1, LedgerTypeRent)
	same := CollectionPayment{MpesaReceipt: "RACE" + uuid.NewString()[:8], Amount: biMoney(t, "700"), Msisdn: "0722000001",
		AccountReference: "BANK-ACC-1", ChannelID: channel, ReceivedAt: time.Now()}
	var wg sync.WaitGroup
	results := make([]*IngestResult, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = models.Collections.Process(ctx, same)
		}()
	}
	wg.Wait()
	fresh := 0
	for i := range results {
		if errs[i] != nil {
			t.Errorf("concurrent delivery %d: %v", i, errs[i])
			continue
		}
		if !results[i].Duplicate {
			fresh++
		}
	}
	if fresh != 1 {
		t.Errorf("%d concurrent deliveries were processed, want exactly 1", fresh)
	}
	wantAfter := biMoney(t, before).Sub(biMoney(t, "700")).String()
	if got := bal(a1, LedgerTypeRent); got != wantAfter {
		t.Errorf("rent after concurrent delivery = %s, want %s (credited once)", got, wantAfter)
	}

	// --- Routing failures record nothing.
	if _, err := models.Collections.Process(ctx, CollectionPayment{MpesaReceipt: "X1", Amount: biMoney(t, "10"), ChannelID: "NO-SUCH-CHANNEL"}); !errors.Is(err, ErrUnroutable) {
		t.Errorf("unknown channel: err = %v, want ErrUnroutable", err)
	}
	if _, err := models.Collections.Process(ctx, CollectionPayment{MpesaReceipt: "X2", Amount: biMoney(t, "10")}); !errors.Is(err, ErrUnroutable) {
		t.Errorf("no channel: err = %v, want ErrUnroutable", err)
	}
	if _, err := models.Collections.Process(ctx, CollectionPayment{MpesaReceipt: "", Amount: biMoney(t, "10"), ChannelID: channel}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("no receipt: err = %v, want ErrInvalidInput", err)
	}
	other, otherLandlord := seedTenant(t, conn)
	shared := &Property{LandlordID: otherLandlord, Name: "Other", Location: "X", Slug: "o-" + uuid.NewString()[:8], PayheroChannelID: &channel}
	if err := models.Properties.Insert(ctx, other, shared); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Collections.Process(ctx, CollectionPayment{MpesaReceipt: "X3", Amount: biMoney(t, "10"), Msisdn: "0722000001", ChannelID: channel}); !errors.Is(err, ErrUnroutable) {
		t.Errorf("a channel shared by two managers: err = %v, want ErrUnroutable", err)
	}

	// --- The review-queue gauge counts what the cases above left for a person:
	// the stranger's unmatched payment, and the guessed (waterfall and fuzzy
	// name) placements. Counts are global, so other tests only add to them.
	if d, err := models.Platform.ReviewQueueDepth(ctx); err != nil || d.Unmatched < 2 || d.Unconfirmed < 2 {
		t.Errorf("ReviewQueueDepth = %+v, %v; want at least 2 unmatched and 2 unconfirmed", d, err)
	}

	// --- The resolver's other lookups.
	target, err := models.Resolver.PayTarget(ctx, property.Slug, "A2")
	if err != nil || target.TenantID != tenant || target.UnitID != a2.ID || target.PropertyID != property.ID {
		t.Errorf("PayTarget = %+v, %v", target, err)
	}
	if _, err := models.Resolver.PayTarget(ctx, property.Slug, "NOPE"); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("unknown unit: err = %v", err)
	}
}
