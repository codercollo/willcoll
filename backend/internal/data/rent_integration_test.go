package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/google/uuid"
)

// TestRentIntegration drives the whole rent domain against real Postgres as
// willcoll_app: expected rent, monthly runs (idempotent, snapshotting,
// concurrent-safe), manual payments through the zero-sum posting path,
// storno reversals, the running-balance ledger and the collection overview.
func TestRentIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	models.Rent.Now = func() time.Time { return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) }

	tenant, landlord := seedTenant(t, conn)
	other, _ := seedTenant(t, conn)

	property := &Property{LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "ra-" + uuid.NewString()[:8]}
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
	lease := func(u *Unit, name, rent string, start time.Time, deposit string) {
		l := &Lease{UnitID: u.ID, TenantName: name, PrimaryPhone: "+254722000000", RentAmount: biMoney(t, rent),
			RentDepositAmount: biMoney(t, deposit), StartDate: start, Status: "active"}
		if err := models.Leases.Insert(ctx, tenant, tenant, l); err != nil {
			t.Fatal(err)
		}
	}
	a1, a2, a3, a4 := newUnit("A1"), newUnit("A2"), newUnit("A3"), newUnit("A4") // A4 stays vacant
	lease(a1, "JOHN KAMAU", "5000", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "5000")
	lease(a2, "ACME LTD", "8000", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "0")
	lease(a3, "NEW TENANT", "4000", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "0") // starts after September

	sep, oct, nov, dec := biPeriod(t, "2026-09"), biPeriod(t, "2026-10"), biPeriod(t, "2026-11"), biPeriod(t, "2026-12")
	overview := func(p string) map[string]OverviewRow {
		rows, err := models.Rent.GetPropertyOverview(ctx, tenant, property.ID, biPeriod(t, p))
		if err != nil {
			t.Fatalf("overview: %v", err)
		}
		m := map[string]OverviewRow{}
		for _, r := range rows {
			m[r.UnitCode] = r
		}
		return m
	}

	// --- (A) Expected rent.
	if err := models.Rent.SetExpectedRent(ctx, tenant, a1.ID, biMoney(t, "0")); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("zero rent: err = %v", err)
	}
	if err := models.Rent.SetExpectedRent(ctx, tenant, a4.ID, biMoney(t, "1000")); !errors.Is(err, ErrNoActiveLease) {
		t.Errorf("vacant unit: err = %v, want ErrNoActiveLease", err)
	}
	// One bad row rolls the whole spreadsheet save back.
	err := models.Rent.BulkSetExpectedRent(ctx, tenant, property.ID, []UnitRentInput{
		{UnitID: a1.ID, RentAmount: biMoney(t, "9999")}, {UnitID: a4.ID, RentAmount: biMoney(t, "1000")},
	})
	if !errors.Is(err, ErrNoActiveLease) {
		t.Fatalf("bulk with a bad row: err = %v", err)
	}
	if got := overview("2026-09")["A1"].Expected; got != biMoney(t, "5000") {
		t.Errorf("A1 expected after failed bulk = %s, want 5000.00 (atomic)", got)
	}
	if err := models.Rent.BulkSetExpectedRent(ctx, other, property.ID, nil); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant bulk set: err = %v", err)
	}
	if err := models.Rent.BulkSetExpectedRent(ctx, tenant, property.ID, []UnitRentInput{
		{UnitID: a1.ID, RentAmount: biMoney(t, "6000")}, {UnitID: a2.ID, RentAmount: biMoney(t, "8000")},
	}); err != nil {
		t.Fatalf("bulk set: %v", err)
	}
	if bal := biBalance(t, conn, tenant, a1.ID, LedgerTypeRent); !bal.IsZero() {
		t.Errorf("setting expected rent must post nothing, balance = %s", bal)
	}

	// --- (B) Monthly run.
	if _, err := models.Rent.GenerateForPeriod(ctx, tenant, tenant, property.ID, dec); !errors.Is(err, ErrPeriodTooFar) {
		t.Errorf("December from September: err = %v, want ErrPeriodTooFar", err)
	}
	if _, err := models.Rent.GenerateForPeriod(ctx, other, other, property.ID, sep); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant run: err = %v", err)
	}
	sum, err := models.Rent.GenerateForPeriod(ctx, tenant, tenant, property.ID, sep)
	if err != nil || sum.Billed != 2 || sum.Skipped != 1 || sum.Total != biMoney(t, "14000") {
		t.Fatalf("September run = %+v, %v", sum, err)
	}
	again, err := models.Rent.GenerateForPeriod(ctx, tenant, tenant, property.ID, sep)
	if err != nil || again.Billed != 0 || again.Skipped != 3 || !again.Total.IsZero() {
		t.Errorf("re-run must bill nobody: %+v, %v", again, err)
	}
	if bal := biBalance(t, conn, tenant, a1.ID, LedgerTypeRent); bal != biMoney(t, "6000") {
		t.Errorf("A1 balance = %s, want 6000.00 (billed once)", bal)
	}

	// The run snapshotted 6000; a later rent change must not rewrite September.
	if err := models.Rent.SetExpectedRent(ctx, tenant, a1.ID, biMoney(t, "7000")); err != nil {
		t.Fatal(err)
	}
	if o := overview("2026-09")["A1"]; o.Expected != biMoney(t, "6000") || !o.Billed {
		t.Errorf("September must keep its snapshot: %+v", o)
	}
	if o := overview("2026-10")["A1"]; o.Expected != biMoney(t, "7000") || o.Billed {
		t.Errorf("October preview should use the new rent, unbilled: %+v", o)
	}

	// --- Manual payments through the zero-sum path.
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, a1.ID, PaymentInput{Amount: biMoney(t, "0"), Source: "manual"}); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("zero payment: err = %v", err)
	}
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, a1.ID, PaymentInput{Amount: biMoney(t, "10"), Source: "mpesa"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad source: err = %v", err)
	}
	if _, err := models.Rent.PostPayment(ctx, other, other, a1.ID, PaymentInput{Amount: biMoney(t, "10"), Source: "manual"}); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant payment: err = %v", err)
	}
	p1, err := models.Rent.PostPayment(ctx, tenant, tenant, a1.ID, PaymentInput{Amount: biMoney(t, "2500"), Source: "manual", Reference: "CASH-1", Note: "rent part"})
	if err != nil {
		t.Fatalf("PostPayment: %v", err)
	}
	if !strings.HasPrefix(p1.Receipt, "MANUAL-") {
		t.Errorf("receipt = %q", p1.Receipt)
	}
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, a1.ID, PaymentInput{Amount: biMoney(t, "2500"), Source: "manual", Reference: "CASH-1"}); !errors.Is(err, ErrDuplicatePayment) {
		t.Errorf("duplicate reference: err = %v, want ErrDuplicatePayment", err)
	}
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, a2.ID, PaymentInput{Amount: biMoney(t, "9000"), Source: "bank", Reference: "KCB-77"}); err != nil {
		t.Fatal(err)
	}
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		var status string
		var allocated, amount string
		if err := tx.QueryRow(`SELECT p.status, p.amount::text, (SELECT SUM(amount)::text FROM payment_allocations WHERE payment_id = p.id)
			FROM payments p WHERE p.id = $1`, p1.PaymentID).Scan(&status, &amount, &allocated); err != nil {
			t.Fatal(err)
		}
		if status != "allocated" || amount != allocated {
			t.Errorf("payment status %q amount %s allocated %s", status, amount, allocated)
		}
	})

	// --- (D) Overview.
	ov := overview("2026-09")
	if len(ov) != 3 { // A1, A2, A3: occupied with an active lease; A4 is vacant
		t.Errorf("overview rows = %d, want 3", len(ov))
	}
	if o := ov["A1"]; o.Paid != biMoney(t, "2500") || o.Balance != biMoney(t, "3500") || o.Status != RentStatusPartial || o.TenantName != "JOHN KAMAU" {
		t.Errorf("A1 = %+v", o)
	}
	if o := ov["A2"]; o.Balance != biMoney(t, "-1000") || o.Status != RentStatusPaid {
		t.Errorf("A2 overpaid must be in credit and paid: %+v", o)
	}
	if o := ov["A3"]; o.Billed || o.Expected != biMoney(t, "4000") {
		t.Errorf("A3 = %+v", o)
	}
	if _, err := models.Rent.GetPropertyOverview(ctx, other, property.ID, sep); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant overview: err = %v", err)
	}

	// --- The Properties card: 3 occupied, 1 vacant; expected is A1's billed
	// 6,000 (its snapshot, not the later 7,000 rent) + A2 8,000 + A3 4,000
	// (not yet billed, so the lease rent); collected is 2,500 + 9,000.
	cards := []*Property{property}
	if err := models.Properties.AttachSummaries(ctx, tenant, sep, cards); err != nil {
		t.Fatal(err)
	}
	if c := cards[0].Summary; c == nil || c.LandlordName == "" || c.UnitsOccupied != 3 || c.UnitsVacant != 1 ||
		c.RentExpected != biMoney(t, "18000") || c.RentCollected != biMoney(t, "11500") {
		t.Errorf("property summary = %+v", c)
	}

	// --- (C) Ledger with running balance; deposits on the same tab.
	filters := Filters{Page: 1, PageSize: 50, Sort: "created_at"}
	view, err := models.Rent.GetUnitLedger(ctx, tenant, a1.ID, filters)
	if err != nil || len(view.Rent.Entries) != 2 || len(view.RentDeposit.Entries) != 1 {
		t.Fatalf("A1 ledger = %+v, %v", view, err)
	}
	last := view.Rent.Entries[len(view.Rent.Entries)-1]
	if last.RunningBalance != view.Rent.Balance.Amount || last.RunningBalance != biMoney(t, "3500") {
		t.Errorf("last running balance %s vs balance %s", last.RunningBalance, view.Rent.Balance.Amount)
	}
	var payEntry, chargeEntry uuid.UUID
	for _, e := range view.Rent.Entries {
		if e.Direction == DirectionCredit {
			payEntry = e.ID
		} else {
			chargeEntry = e.ID
		}
	}

	// --- Storno: reverse the payment.
	if _, err := models.Rent.ReverseEntry(ctx, tenant, tenant, payEntry, "  "); !errors.Is(err, ErrReasonRequired) {
		t.Errorf("blank reason: err = %v", err)
	}
	if _, err := models.Rent.ReverseEntry(ctx, other, other, payEntry, "x"); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another tenant reversal: err = %v", err)
	}
	revID, err := models.Rent.ReverseEntry(ctx, tenant, tenant, payEntry, "cheque bounced")
	if err != nil {
		t.Fatalf("ReverseEntry: %v", err)
	}
	if bal := biBalance(t, conn, tenant, a1.ID, LedgerTypeRent); bal != biMoney(t, "6000") {
		t.Errorf("balance after reversing the payment = %s, want 6000.00", bal)
	}
	if _, err := models.Rent.ReverseEntry(ctx, tenant, tenant, payEntry, "again"); !errors.Is(err, ErrAlreadyReversed) {
		t.Errorf("second reversal: err = %v, want ErrAlreadyReversed", err)
	}
	if _, err := models.Rent.ReverseEntry(ctx, tenant, tenant, revID, "undo"); !errors.Is(err, ErrNotReversible) {
		t.Errorf("reversing a reversal: err = %v, want ErrNotReversible", err)
	}
	view, _ = models.Rent.GetUnitLedger(ctx, tenant, a1.ID, filters)
	if len(view.Rent.Entries) != 3 {
		t.Fatalf("both the mistake and the storno must stay visible: %d entries", len(view.Rent.Entries))
	}
	for _, e := range view.Rent.Entries {
		switch e.ID {
		case payEntry:
			if !e.Reversed {
				t.Error("original payment must be flagged reversed")
			}
		case revID:
			if e.ReferenceType != ReferenceTypeReversal || e.ReferenceID != payEntry || e.Direction != DirectionDebit ||
				!strings.Contains(e.Description, "cheque bounced") || !strings.Contains(e.Description, payEntry.String()) {
				t.Errorf("reversal entry = %+v", e)
			}
		}
	}
	// Reports and the overview must no longer count the reversed payment.
	if o := overview("2026-09")["A1"]; !o.Paid.IsZero() || o.Status != RentStatusArrears {
		t.Errorf("reversed payment still counted: %+v", o)
	}
	rep, err := models.Reports.Preview(ctx, tenant, property.ID, sep)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Rows {
		if r.HouseNo == "A1" && len(r.Rent) != 0 {
			t.Errorf("schedule must exclude the reversed payment: %+v", r.Rent)
		}
	}

	cards[0].Summary = nil
	if err := models.Properties.AttachSummaries(ctx, tenant, sep, cards); err != nil || cards[0].Summary.RentCollected != biMoney(t, "9000") {
		t.Errorf("after the reversal the card must collect 9000.00: %+v, %v", cards[0].Summary, err)
	}

	// Correct a wrong charge: reverse it, then post the corrected amount.
	if _, err := models.Rent.ReverseEntry(ctx, tenant, tenant, chargeEntry, "wrong amount"); err != nil {
		t.Fatal(err)
	}
	if err := models.Rent.PostManualCharge(ctx, tenant, tenant, a1.ID, LedgerTypeRent, biMoney(t, "6500"), "Corrected September rent"); err != nil {
		t.Fatal(err)
	}
	if bal := biBalance(t, conn, tenant, a1.ID, LedgerTypeRent); bal != biMoney(t, "6500") {
		t.Errorf("balance after storno + corrected charge = %s, want 6500.00", bal)
	}
	if err := models.Rent.PostManualCharge(ctx, tenant, tenant, a1.ID, LedgerTypeRent, biMoney(t, "5"), " "); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("charge without description: err = %v", err)
	}

	// --- Concurrency.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = models.Rent.ReverseEntry(ctx, tenant, tenant, chargeEntry, "race")
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if !errors.Is(e, ErrAlreadyReversed) {
			t.Errorf("reversing an already-reversed entry concurrently: err = %v", e)
		}
	}

	before := biBalance(t, conn, tenant, a2.ID, LedgerTypeRent)
	billed := make([]int, 4)
	runErrs := make([]error, 4)
	for i := range billed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var s *RunSummary
			s, runErrs[i] = models.Rent.GenerateForPeriod(ctx, tenant, tenant, property.ID, oct)
			if s != nil {
				billed[i] = s.Billed
			}
		}()
	}
	wg.Wait()
	total := 0
	for i, n := range billed {
		if runErrs[i] != nil {
			t.Errorf("concurrent run %d: %v", i, runErrs[i])
		}
		total += n
	}
	// A1, A2 and A3 (its lease starts on 15 October, inside the period) are
	// each billed exactly once across all four concurrent runs.
	if total != 3 {
		t.Errorf("concurrent October runs billed %d units in total, want 3", total)
	}
	if bal := biBalance(t, conn, tenant, a2.ID, LedgerTypeRent); bal != before.Add(biMoney(t, "8000")) {
		t.Errorf("A2 after concurrent runs = %s, want %s (billed once)", bal, before.Add(biMoney(t, "8000")))
	}
	if _, err := models.Rent.GenerateForPeriod(ctx, tenant, tenant, property.ID, nov); !errors.Is(err, ErrPeriodTooFar) {
		t.Errorf("November from a September clock: err = %v, want ErrPeriodTooFar", err)
	}
}
