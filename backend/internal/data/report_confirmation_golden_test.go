package data

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

var goldenTjRe = regexp.MustCompile(`\((.*)\) Tj`)

// extractPDFText returns every text run in an uncompressed maroto PDF, one
// per line (mirrors internal/pdf's own pdfText test helper, unexported and
// so not reusable across packages).
func extractPDFText(t *testing.T, b []byte) string {
	t.Helper()
	if !strings.HasPrefix(string(b), "%PDF") {
		t.Fatal("output is not a PDF")
	}
	var out []string
	for _, m := range goldenTjRe.FindAllStringSubmatch(string(b), -1) {
		s := strings.NewReplacer(`\(`, "(", `\)`, ")", `\`, `\`).Replace(m[1])
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

// TestRundasAugustGolden confirms August 2026 for a property whose numbers
// mirror THE RUNDAS TOTAL PAYMENTS AUGUST 2026 reference document — total
// rent collected 567,000 (management fee 28,350, not the source document's
// arithmetic slip of 28,360, per the "exact arithmetic" rule), water 162
// units at KSH. 150 billed 24,300 against 23,650 actually paid (deviation
// "PAID LESS KSH. 650"), and 20+20 storage against a 202-unit plot-meter
// reading (deviation "202-162 = 40") — then renders the confirmed schedule
// PDF and asserts NOTE:1 matches the source document's wording verbatim.
//
// This uses one aggregate unit rather than the source's 48, since the
// FIFO/arrears/advance derivation is already covered unit-by-unit in
// TestReportConfirmationIntegration; this test is specifically about
// NOTE:1's numbers and wording, and the confirmed-snapshot PDF pipeline
// end to end.
func TestRundasAugustGolden(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "The Rundas Arcade", Location: "Kasarani", Slug: "rundas-golden-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"), ManagementFeePercent: 5,
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	property.UndergroundCapacityUnits = ptrMoney(biMoney(t, "20"))
	property.RooftopCapacityUnits = ptrMoney(biMoney(t, "20"))
	if err := models.Properties.Update(ctx, property); err != nil {
		t.Fatalf("set storage capacity: %v", err)
	}

	unit := &Unit{PropertyID: property.ID, UnitCode: "G1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "RUNDAS TENANTS AGGREGATE", PrimaryPhone: "+254700000000",
		RentAmount: biMoney(t, "567000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	aug := biPeriod(t, "2026-08")
	rcCharge(t, conn, tenant, unit.ID, biMoney(t, "567000"), aug.FirstDay(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	rcPay(t, conn, tenant, unit.ID, biMoney(t, "567000"), time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))

	if err := models.Water.SaveReadings(ctx, tenant, tenant, property.ID, aug, []WaterReadingInput{{UnitID: unit.ID, CurrentReading: biMoney(t, "162")}}); err != nil {
		t.Fatalf("water reading: %v", err)
	}
	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, aug); err != nil {
		t.Fatalf("water run: %v", err)
	}
	// Tenants actually paid 23,650 against the 24,300 billed (162 x 150) —
	// the source document's "PAID LESS KSH. 650" deviation. PostPayment
	// stamps received_at from RentModel.Now, which defaults to the real
	// clock; without overriding it here the payment lands outside August
	// and ListPeriodPayments silently excludes it from the totals.
	models.Rent.Now = func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) }
	if _, err := models.Rent.PostPayment(ctx, tenant, tenant, unit.ID, PaymentInput{
		Amount: biMoney(t, "23650"), Source: "manual", LedgerType: LedgerTypeWater,
	}); err != nil {
		t.Fatalf("water payment: %v", err)
	}
	zeroReading := biMoney(t, "0")
	if err := models.Reports.SetPlotMeterReading(ctx, tenant, tenant, property.ID, aug, biMoney(t, "202"), &zeroReading, time.Time{}); err != nil {
		t.Fatalf("plot meter reading: %v", err)
	}

	result, _, err := models.Reports.Checks(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if !result.Passed() {
		t.Fatalf("expected checks to pass, got: %+v", result.Failures)
	}

	confirmedBy := tenant
	if _, err := models.Reports.Confirm(ctx, tenant, confirmedBy, property.ID, aug); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	report, err := models.Reports.GenerateConfirmed(ctx, tenant, property.ID, aug)
	if err != nil {
		t.Fatalf("GenerateConfirmed: %v", err)
	}

	b, err := pdf.BuildMonthlySchedule(report.Schedule())
	if err != nil {
		t.Fatalf("BuildMonthlySchedule: %v", err)
	}
	text := extractPDFText(t, b)

	for _, want := range []string{
		"THE RUNDAS ARCADE", "KASARANI",
		"ALL IN ONE PAYMENTS SCHEDULE: MONTH: AUGUST 2026",
		"TOTAL WATER UNITS CONSUMED BY TENANTS: 162",
		"WATER PAYMENT RATE PER UNIT: KSH. 150",
		"EXPECTED TOTAL WATER BILLS PAYMENT BY TENANTS: KSH. 24,300",
		"TOTAL WATER BILLS PAYMENT: KSH. 23,650",
		"THE DEVIATION/ THE DIFFERENCE OF WATER BILLED AND WATER PAID: - (VE) MEANING, PAID LESS KSH. 650",
		"UNDERGROUND WATER STORAGE CAPACITY IN UNITS: 20",
		"ROOFTOP WATER TANKS CAPACITY IN UNITS: 20",
		"TOTAL WATER STORAGE IN UNITS: 40",
		"TOTAL WATER PASSED THROUGH THE PLOT METER IN UNITS: 202",
		"BOTH TOTALS OF WATER BILLED AND THE WATER IN STORAGE IN UNITS: 202",
		"THE DEVIATION/ THE DIFFERENCE IN UNITS OF WATER IN THE STORAGE AND THE WATER BILLED FOR TENANTS: 202-162 = 40",
		// Exact arithmetic: 5/100 x 567,000 = 28,350 — not the source
		// document's typo of 28,360.
		"MANAGEMENT FEE: 5/100 X KSH. 567,000 = KSHS. 28,350",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF missing %q\n---\n%s", want, text)
		}
	}
	if strings.Contains(text, "28,360") {
		t.Error("PDF reproduced the source document's management-fee arithmetic slip (28,360)")
	}

	// Render one of each PDF type from this same fixture, for a human to
	// eyeball before Phase E.
	receipts, err := models.Receipts.Generate(ctx, tenant, property.ID, aug, &unit.ID)
	if err != nil || len(receipts) == 0 {
		t.Fatalf("Receipts.Generate: %v, %d receipts", err, len(receipts))
	}
	receiptPDF, err := pdf.BuildReceipts(receipts)
	if err != nil {
		t.Fatalf("BuildReceipts: %v", err)
	}

	invoices, err := models.Water.Invoices(ctx, tenant, property.ID, aug, &unit.ID)
	if err != nil || len(invoices) == 0 {
		t.Fatalf("Water.Invoices: %v, %d invoices", err, len(invoices))
	}
	billingPDF, err := pdf.BuildWaterInvoice(invoices[0])
	if err != nil {
		t.Fatalf("BuildWaterInvoice: %v", err)
	}

	dir := filepath.Join(os.TempDir(), "willcoll-rundas-fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	files := map[string][]byte{
		"schedule.pdf": b,
		"receipt.pdf":  receiptPDF,
		"billing.pdf":  billingPDF,
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
	}
}

func ptrMoney(m moneyfmt.Money) *moneyfmt.Money { return &m }
