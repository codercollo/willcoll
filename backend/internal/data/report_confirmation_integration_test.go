package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// rcCharge posts a rent DEBIT with an explicit created_at, so a test can
// narrate a historical calendar period without needing to UPDATE
// ledger_entries afterwards — which willcoll_app cannot do (the append-only
// grants in migration 000018 revoke UPDATE/DELETE on it, correctly, so a
// test must not work around that either).
func rcCharge(t *testing.T, conn *sql.DB, tenant, unit uuid.UUID, amount moneyfmt.Money, period, postedAt time.Time) {
	t.Helper()
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		exec := func(q string, args ...any) {
			if _, err := tx.Exec(q, args...); err != nil {
				t.Fatalf("%v\n%s", err, q)
			}
		}
		var account uuid.UUID
		if err := tx.QueryRow(`
			INSERT INTO ledger_accounts (tenant_id, unit_id, type) VALUES ($1, $2, 'RENT')
			ON CONFLICT (unit_id, type) DO UPDATE SET type = EXCLUDED.type RETURNING id`,
			tenant, unit).Scan(&account); err != nil {
			t.Fatal(err)
		}
		charge, header, entry := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO charges (id, tenant_id, ledger_account_id, source_type, period, amount, description, created_by, created_at)
			VALUES ($1, $2, $3, 'manual', $4, $5, 'rent charge', $6, $7)`,
			charge, tenant, account, period, amount, tenant, postedAt)
		exec(`INSERT INTO transaction_headers (id, tenant_id, type, idempotency_key, created_by, created_at)
			VALUES ($1, $2, 'MANUAL_ADJUSTMENT', $3, 'test', $4)`,
			header, tenant, "charge:"+charge.String(), postedAt)
		exec(`INSERT INTO ledger_entries (id, tenant_id, transaction_header_id, ledger_account_id, direction, amount, reference_type, reference_id, created_at)
			VALUES ($1, $2, $3, $4, 'DEBIT', $5, 'charge', $6, $7)`,
			entry, tenant, header, account, amount, charge, postedAt)
	})
}

// rcPay posts a rent CREDIT (a payment allocation) with an explicit
// created_at, for the same reason as rcCharge.
func rcPay(t *testing.T, conn *sql.DB, tenant, unit uuid.UUID, amount moneyfmt.Money, postedAt time.Time) {
	rcPayLedger(t, conn, tenant, unit, "RENT", amount, postedAt)
}

// rcPayLedger is rcPay generalised to any ledger type (RENT, WATER,
// GARBAGE, ...), for fixtures that need payments across several types.
func rcPayLedger(t *testing.T, conn *sql.DB, tenant, unit uuid.UUID, ledgerType string, amount moneyfmt.Money, postedAt time.Time) {
	t.Helper()
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		exec := func(q string, args ...any) {
			if _, err := tx.Exec(q, args...); err != nil {
				t.Fatalf("%v\n%s", err, q)
			}
		}
		var account uuid.UUID
		if err := tx.QueryRow(`
			INSERT INTO ledger_accounts (tenant_id, unit_id, type) VALUES ($1, $2, $3)
			ON CONFLICT (unit_id, type) DO UPDATE SET type = EXCLUDED.type RETURNING id`,
			tenant, unit, ledgerType).Scan(&account); err != nil {
			t.Fatal(err)
		}
		payment, header, entry, alloc := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO payments (id, tenant_id, source, mpesa_receipt, amount, msisdn, matched_unit_id, status, raw_payload, received_at)
			VALUES ($1, $2, 'manual', $3, $4, '+254700000000', $5, 'allocated', '{}', $6)`,
			payment, tenant, "R"+payment.String()[:9], amount, unit, postedAt)
		exec(`INSERT INTO transaction_headers (id, tenant_id, type, idempotency_key, created_by, created_at)
			VALUES ($1, $2, 'PAYMENT_POSTING', $3, 'test', $4)`,
			header, tenant, "pay:"+payment.String(), postedAt)
		exec(`INSERT INTO ledger_entries (id, tenant_id, transaction_header_id, ledger_account_id, direction, amount, reference_type, reference_id, created_at)
			VALUES ($1, $2, $3, $4, 'CREDIT', $5, 'payment_allocation', $6, $7)`,
			entry, tenant, header, account, amount, alloc, postedAt)
		exec(`INSERT INTO payment_allocations (id, tenant_id, payment_id, ledger_account_id, ledger_entry_id, amount)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			alloc, tenant, payment, account, entry, amount)
	})
}

// TestReportConfirmationIntegration drives period confirmation against real
// Postgres: the FIFO-derived NOTE:2 for a one-month advance (Mark) and a
// multi-month lump-sum advance (Jane), the blocking water-billed-vs-rate
// check, and the stale-after-edit / 409-shaped sentinel errors.
func TestReportConfirmationIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "The Rundas Arcade", Location: "Kasarani", Slug: "rundas-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"), ManagementFeePercent: 5,
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
	g1, oneA, vacant := newUnit("G1"), newUnit("1A"), newUnit("V1")

	leaseStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newLease := func(u *Unit, name, rent string) {
		l := &Lease{UnitID: u.ID, TenantName: name, PrimaryPhone: "+254700000000",
			RentAmount: biMoney(t, rent), StartDate: leaseStart, Status: "active"}
		if err := models.Leases.Insert(ctx, tenant, tenant, l); err != nil {
			t.Fatal(err)
		}
	}
	newLease(g1, "MARK AUMA ADONDO", "10000")
	newLease(oneA, "JANE WAMBUI", "12000")

	jul, aug := biPeriod(t, "2026-07"), biPeriod(t, "2026-08")

	rcCharge(t, conn, tenant, g1.ID, biMoney(t, "10000"), jul.FirstDay(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	rcCharge(t, conn, tenant, oneA.ID, biMoney(t, "12000"), jul.FirstDay(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	rcPay(t, conn, tenant, g1.ID, biMoney(t, "10000"), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC))
	rcPay(t, conn, tenant, oneA.ID, biMoney(t, "72000"), time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC))

	rcCharge(t, conn, tenant, g1.ID, biMoney(t, "10000"), aug.FirstDay(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	rcCharge(t, conn, tenant, oneA.ID, biMoney(t, "12000"), aug.FirstDay(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	rcPay(t, conn, tenant, g1.ID, biMoney(t, "20000"), time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC))

	// Water: consumption x current rate must equal what's billed, so the
	// blocking check passes for this property/period.
	if err := models.Water.SaveReadings(ctx, tenant, tenant, property.ID, aug, []WaterReadingInput{{UnitID: g1.ID, CurrentReading: biMoney(t, "10")}}); err != nil {
		t.Fatalf("water reading: %v", err)
	}
	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, aug); err != nil {
		t.Fatalf("August water run: %v", err)
	}

	_ = vacant // stays vacant on purpose: occupied + vacant must equal total units

	result, report, err := models.Reports.Checks(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if !result.Passed() {
		t.Fatalf("expected checks to pass, got failures: %+v", result.Failures)
	}
	if report.Summary.Occupied != 2 || report.Summary.Vacant != 1 {
		t.Fatalf("occupied/vacant = %d/%d, want 2/1", report.Summary.Occupied, report.Summary.Vacant)
	}

	confirmedBy := tenant
	confirmed, err := models.Reports.Confirm(ctx, tenant, confirmedBy, property.ID, aug)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	joined := strings.Join(confirmed.Notes, " | ")
	if !strings.Contains(joined, "MARK AUMA ADONDO, HSE NO. G1. SEPTEMBER 2026 FULL RENT OF KSH. 10,000 PAID IN ADVANCE.") {
		t.Errorf("Mark's advance note missing or wrong, got: %s", joined)
	}
	if !strings.Contains(joined, "JANE WAMBUI, HSE NO. 1A. RENT PAID IN ADVANCE FOR SEPTEMBER 2026, OCTOBER 2026, NOVEMBER 2026 AND DECEMBER 2026, AMOUNTING TO KSH. 48,000.") {
		t.Errorf("Jane's advance note missing or wrong, got: %s", joined)
	}

	// Re-requesting the confirmed PDF's data reads the frozen snapshot.
	confirmedAgain, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("GenerateConfirmed: %v", err)
	}
	if strings.Join(confirmedAgain.Notes, " | ") != joined {
		t.Errorf("GenerateConfirmed notes drifted from the frozen snapshot")
	}

	// A ledger change after confirmation makes the period stale, even a
	// backdated correction with no matching payment (it shifts NOTE:2's
	// balance, not any printed total).
	rcCharge(t, conn, tenant, g1.ID, biMoney(t, "1"), aug.FirstDay(), time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC))
	if _, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, aug); !errors.Is(err, ErrPeriodStale) {
		t.Errorf("after ledger edit: err = %v, want ErrPeriodStale", err)
	}

	// An unconfirmed period, e.g. next month, is rejected outright.
	sep := biPeriod(t, "2026-09")
	if _, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, sep); !errors.Is(err, ErrPeriodNotConfirmed) {
		t.Errorf("unconfirmed period: err = %v, want ErrPeriodNotConfirmed", err)
	}
}

// TestReportConfirmationBlocksOnWaterMismatch covers the blocking check: a
// reading billed at a rate that no longer matches the property's current
// rate (a real scenario — the rate changed after the reading was locked)
// must block confirmation, distinct from the informational paid-vs-billed
// deviation that must never block.
func TestReportConfirmationBlocksOnWaterMismatch(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Mismatch Property", Location: "Nairobi", Slug: "mismatch-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"),
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "B1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "TEST TENANT", PrimaryPhone: "+254700000000",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	aug := biPeriod(t, "2026-08")
	if err := models.Water.SaveReadings(ctx, tenant, tenant, property.ID, aug, []WaterReadingInput{{UnitID: unit.ID, CurrentReading: biMoney(t, "10")}}); err != nil {
		t.Fatalf("water reading: %v", err)
	}
	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, aug); err != nil {
		t.Fatalf("water run: %v", err)
	}
	// Billed 10 x 150 = 1,500. Now the property's rate changes, so the
	// live "expected" recomputes at the new rate while the already-billed
	// reading keeps its snapshot rate.
	current, err := models.Properties.Get(ctx, tenant, property.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.WaterRatePerUnit = biMoney(t, "200")
	if err := models.Properties.Update(ctx, current); err != nil {
		t.Fatalf("rate change: %v", err)
	}

	result, _, err := models.Reports.Checks(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if result.Passed() {
		t.Fatal("expected the water-billed-vs-rate check to block, but it passed")
	}
	found := false
	for _, f := range result.Failures {
		if f.Code == "water_billed_mismatch" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a water_billed_mismatch failure, got: %+v", result.Failures)
	}

	if _, err := models.Reports.Confirm(ctx, tenant, tenant, property.ID, aug); err == nil {
		t.Fatal("Confirm should have failed the sanity check")
	} else if _, ok := err.(*SanityCheckError); !ok {
		t.Errorf("Confirm error = %T, want *SanityCheckError", err)
	}
}

// TestNote2ArrearsWording is the 2026-09-28 wording fix: no more "(NEW)"/
// "(CARRIED)" suffixes, and an arrears balance spanning several months
// prints the month range it actually covers instead of just labeling the
// whole thing with the current period — the real KIWI PLACE B3 case this
// mirrors: 10,500 rent/month, a 3,500 partial payment in July and August,
// nothing in September, leaving 24,500 owed across July-September.
func TestNote2ArrearsWording(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Arrears Wording Property", Location: "Nairobi", Slug: "arrears-wording-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"),
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "B3", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "TABITHA NYAMBURA KIMANI", PrimaryPhone: "+254700000000",
		RentAmount: biMoney(t, "10500"), StartDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	jul := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	sep := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rcCharge(t, conn, tenant, unit.ID, biMoney(t, "10500"), jul, jul.AddDate(0, 0, 1))
	rcPay(t, conn, tenant, unit.ID, biMoney(t, "3500"), jul.AddDate(0, 0, 13))
	rcCharge(t, conn, tenant, unit.ID, biMoney(t, "10500"), aug, aug.AddDate(0, 0, 1))
	rcPay(t, conn, tenant, unit.ID, biMoney(t, "3500"), aug.AddDate(0, 0, 15))
	rcCharge(t, conn, tenant, unit.ID, biMoney(t, "10500"), sep, sep.AddDate(0, 0, 1))

	sepPeriod := biPeriod(t, "2026-09")
	_, report, err := models.Reports.Checks(ctx, tenant, property.ID, sepPeriod)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if len(report.Notes) != 1 {
		t.Fatalf("NOTE:2 = %v, want exactly one arrears line", report.Notes)
	}
	want := "TABITHA NYAMBURA KIMANI, HSE NO. B3. HAS RENT ARREARS OF KSH. 24,500 FOR JULY - SEPTEMBER 2026."
	if report.Notes[0] != want {
		t.Errorf("NOTE:2 line = %q, want %q", report.Notes[0], want)
	}
	for _, suffix := range []string{"(NEW)", "(CARRIED)"} {
		if strings.Contains(report.Notes[0], suffix) {
			t.Errorf("NOTE:2 line still has the dropped %q suffix: %q", suffix, report.Notes[0])
		}
	}
}

// TestReportChecksFlagsImplausiblePlotMeterReading is the 2026-09-27
// regression: a plot meter reading many times larger than the units billed
// (a currency amount typed into the units field) must show up as an
// informational nudge, never block Confirm the way a real ledger error does.
func TestReportChecksFlagsImplausiblePlotMeterReading(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Plot Meter Property", Location: "Nairobi", Slug: "plot-meter-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"),
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "B1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "TEST TENANT", PrimaryPhone: "+254700000000",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	aug := biPeriod(t, "2026-08")
	if err := models.Water.SaveReadings(ctx, tenant, tenant, property.ID, aug, []WaterReadingInput{{UnitID: unit.ID, CurrentReading: biMoney(t, "10")}}); err != nil {
		t.Fatalf("water reading: %v", err)
	}
	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, aug); err != nil {
		t.Fatalf("water run: %v", err)
	}
	// Billed 10 units; a reading of 12,000 looks like a shilling amount
	// mistyped into the units field, not a plausible plot meter reading.
	zeroReading := biMoney(t, "0")
	if err := models.Reports.SetPlotMeterReading(ctx, tenant, tenant, property.ID, aug, biMoney(t, "12000"), &zeroReading, time.Time{}); err != nil {
		t.Fatalf("SetPlotMeterReading: %v", err)
	}

	result, _, err := models.Reports.Checks(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if !result.Passed() {
		t.Fatalf("plot meter sanity must be informational, not blocking: %+v", result.Failures)
	}
	found := false
	for _, i := range result.Info {
		if strings.Contains(i, "check the plot meter reading") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an implausible-plot-meter info line, got: %+v", result.Info)
	}

	if _, err := models.Reports.Confirm(ctx, tenant, tenant, property.ID, aug); err != nil {
		t.Errorf("Confirm should succeed despite the informational nudge: %v", err)
	}
}

// TestReportReconfirmAfterStale drives the exact flow the frontend's
// 409-handling depends on: confirm, post a payment that goes stale,
// GenerateConfirmed 409s ErrPeriodStale, re-confirm re-freezes under a new
// checksum, and GenerateConfirmed then succeeds with the new totals. It
// also checks the superseded confirmation lands in history rather than
// being silently overwritten.
func TestReportReconfirmAfterStale(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Reconfirm Property", Location: "Nairobi", Slug: "reconfirm-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"),
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "C1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "RECONFIRM TENANT", PrimaryPhone: "+254700000000",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	aug := biPeriod(t, "2026-08")
	rcCharge(t, conn, tenant, unit.ID, biMoney(t, "5000"), aug.FirstDay(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	rcPay(t, conn, tenant, unit.ID, biMoney(t, "5000"), time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))

	first, err := models.Reports.Confirm(ctx, tenant, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("first Confirm: %v", err)
	}
	if first.Totals.Rent.Display() != "5,000.00" {
		t.Fatalf("first totals.rent = %s, want 5,000.00", first.Totals.Rent.Display())
	}

	// A late payment (an overpayment/advance) shifts NOTE:2 without
	// touching any printed total, so the period goes stale even though
	// GenerateConfirmed's own totals check alone wouldn't catch it.
	rcPay(t, conn, tenant, unit.ID, biMoney(t, "5000"), time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))

	if _, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, aug); !errors.Is(err, ErrPeriodStale) {
		t.Fatalf("GenerateConfirmed after late payment: err = %v, want ErrPeriodStale", err)
	}

	second, err := models.Reports.Confirm(ctx, tenant, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("re-Confirm: %v", err)
	}
	if second.ConfirmedAt.Equal(first.ConfirmedAt) || !second.ConfirmedAt.After(first.ConfirmedAt) {
		t.Errorf("re-confirm confirmed_at = %v, want after first confirm %v", second.ConfirmedAt, first.ConfirmedAt)
	}

	final, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("GenerateConfirmed after re-confirm: %v", err)
	}
	if final.Totals.Rent.Display() != "10,000.00" {
		t.Fatalf("final totals.rent = %s, want 10,000.00 (both payments received in August)", final.Totals.Rent.Display())
	}
	joined := strings.Join(final.Notes, " | ")
	if !strings.Contains(joined, "RECONFIRM TENANT, HSE NO. C1.") {
		t.Errorf("expected the re-confirmed NOTE:2 to mention the new advance, got: %s", joined)
	}

	var historyCount int
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT count(*) FROM report_confirmation_history WHERE property_id = $1 AND period = $2`,
			property.ID, aug.FirstDay()).Scan(&historyCount); err != nil {
			t.Fatalf("count history rows: %v", err)
		}
	})
	if historyCount != 1 {
		t.Errorf("report_confirmation_history rows = %d, want 1 (the superseded first confirmation)", historyCount)
	}
}
