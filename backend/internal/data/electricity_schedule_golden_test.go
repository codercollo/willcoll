package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/google/uuid"
)

// TestElectricityDepositScheduleGolden is the 2026-09-28 Feature 2 schedule
// requirement: with electricity enabled and two deposit payments in the
// period, the ELECTRICITY DEPOSITS PAID column appears (after WATER
// DEPOSITS PAID, before TOTAL AMOUNTS PAID), each row's total and the grand
// total include it, and the confirmation checksum covers it (a later
// electricity-only change must go stale). The sibling case — disabled, no
// payments, layout identical to today — is already covered by every other
// schedule test staying green unmodified (TestRundasAugustGolden included).
func TestElectricityDepositScheduleGolden(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Kiwi Place", Location: "Kasarani", Slug: "kiwi-electricity-golden-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"), ManagementFeePercent: 5,
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}

	sep := biPeriod(t, "2026-09")
	unitA := &Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	unitB := &Unit{PropertyID: property.ID, UnitCode: "A2", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unitA); err != nil {
		t.Fatal(err)
	}
	if err := models.Units.Insert(ctx, tenant, unitB); err != nil {
		t.Fatal(err)
	}
	leaseA := &Lease{UnitID: unitA.ID, TenantName: "EUNICE WAIRIMU", PrimaryPhone: "+254700000010",
		RentAmount: biMoney(t, "9000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active",
		ElectricityDepositAmount: biMoney(t, "2000")}
	leaseB := &Lease{UnitID: unitB.ID, TenantName: "PETER OMONDI", PrimaryPhone: "+254700000011",
		RentAmount: biMoney(t, "9000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active",
		ElectricityDepositAmount: biMoney(t, "2500")}
	if err := models.Leases.Insert(ctx, tenant, tenant, leaseA); err != nil {
		t.Fatal(err)
	}
	if err := models.Leases.Insert(ctx, tenant, tenant, leaseB); err != nil {
		t.Fatal(err)
	}

	current := property
	current.ElectricityEnabled = true
	current.ElectricityDepositAmount = biMoney(t, "2000")
	if err := models.Properties.Update(ctx, current); err != nil {
		t.Fatalf("enable electricity: %v", err)
	}

	rcPayLedger(t, conn, tenant, unitA.ID, LedgerTypeElectricityDeposit, biMoney(t, "2000"), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	rcPayLedger(t, conn, tenant, unitB.ID, LedgerTypeElectricityDeposit, biMoney(t, "1000"), time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))

	result, report, err := models.Reports.Checks(ctx, tenant, property.ID, sep)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if !report.ElectricityDepositShown {
		t.Fatal("ElectricityDepositShown = false, want true (property has it enabled)")
	}
	if got := report.Totals.ElectricityDeposit.Display(); got != "3,000.00" {
		t.Errorf("Totals.ElectricityDeposit = %s, want 3,000.00", got)
	}
	if got := report.Totals.Grand.Display(); got != "3,000.00" {
		t.Errorf("Totals.Grand = %s, want 3,000.00 (only the two deposit payments this period)", got)
	}
	if !result.Passed() {
		t.Fatalf("expected checks to pass, got: %+v", result.Failures)
	}

	if _, err := models.Reports.Confirm(ctx, tenant, tenant, property.ID, sep); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	confirmed, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, sep)
	if err != nil {
		t.Fatalf("GenerateConfirmed: %v", err)
	}

	b, err := pdf.BuildMonthlySchedule(confirmed.Schedule())
	if err != nil {
		t.Fatalf("BuildMonthlySchedule: %v", err)
	}
	text := extractPDFText(t, b)

	// The header cell's text wraps across several Tj runs at this column
	// width ("ELECTRICITY" / "DEPOSITS" / "PAID" / "(KSHS.)"), so check the
	// words rather than one contiguous phrase; the column's actual
	// left-to-right position (after water deposit, before the total) is
	// fixed by construction in monthly_schedule.go's columns()/tableRow().
	for _, want := range []string{
		"ELECTRICITY", "DEPOSITS", "(KSHS.)",
		"2,000", "1,000", // the two payments
		"DEPOSIT", "PAID.", "3,000", // the totals-row label and sum
		"GRAND TOTAL", "PAYMENT.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF missing %q\n---\n%s", want, text)
		}
	}

	// A later change that touches only the electricity deposit ledger must
	// go stale, proving the checksum covers the new column.
	rcPayLedger(t, conn, tenant, unitB.ID, LedgerTypeElectricityDeposit, biMoney(t, "1500"), time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	if _, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, sep); err == nil {
		t.Error("expected the period to have gone stale after a new electricity deposit payment")
	} else if !errors.Is(err, ErrPeriodStale) {
		t.Errorf("GenerateConfirmed after drift = %v, want ErrPeriodStale", err)
	}
}

// TestPostPaymentAcceptsElectricityDeposit is the 2026-09-28 regression:
// RentModel.PostPayment had its own separate manualLedgerTypes allow-list,
// missed when electricity_deposit was added to the handler-level safelist,
// so a manager recording a manual electricity deposit payment got a 422
// "unknown ledger type" despite the ledger tab and pay page both offering
// it.
func TestPostPaymentAcceptsElectricityDeposit(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "Electricity Payment Property", Location: "Nairobi",
		Slug: "electricity-payment-" + uuid.NewString()[:8], WaterRatePerUnit: biMoney(t, "150"),
		ElectricityEnabled: true, ElectricityDepositAmount: biMoney(t, "2000"),
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "E1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "TEST TENANT", PrimaryPhone: "+254700000020",
		RentAmount: biMoney(t, "5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active",
		ElectricityDepositAmount: biMoney(t, "2000")}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, unit.ID, PaymentInput{
		Amount: biMoney(t, "10"), Source: "manual", LedgerType: LedgerTypeElectricityDeposit, Note: "test",
	}); err != nil {
		t.Fatalf("PostPayment(electricity_deposit): %v", err)
	}

	ul, err := models.Ledger.GetUnitLedger(ctx, tenant, unit.ID, LedgerTypeElectricityDeposit, Filters{Page: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := ul.Balance.Amount.Display(); got != "1,990.00" {
		t.Errorf("balance after a 10 payment on a 2,000 deposit = %s, want 1,990.00", got)
	}
}
