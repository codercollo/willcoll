package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

var periodParam = gin.Param{Key: "period", Value: "2026-09"}

func day(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

func scheduleUnits() []sqlc.ListScheduleUnitsRow {
	return []sqlc.ListScheduleUnitsRow{
		{UnitID: testUnitA, UnitCode: "G1", Status: "occupied", TenantName: "JOHN KAMAU", PayerNames: "MARY WANJIKU\nPETER OMONDI", GarbageBilled: true},
		{UnitID: testUnitB, UnitCode: "SHOP NO.2", Status: "occupied", TenantName: "ACME LTD"},
		{UnitID: uuid.New(), UnitCode: "G3", Status: "vacant"},
	}
}

func payment(unit uuid.UUID, ledger string, d int, amount, source string) sqlc.ListPeriodPaymentsRow {
	return sqlc.ListPeriodPaymentsRow{
		UnitID: unit, LedgerType: ledger, PaidOn: day(d), Amount: money(amount),
		PaymentID: uuid.New(), Source: source,
	}
}

func expectReportInputs(q *mock.MockQuerier, garbageEnabled bool, payments []sqlc.ListPeriodPaymentsRow, notesJSON string) {
	p := testPropertyRow()
	p.GarbageEnabled = garbageEnabled
	q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(p, nil).AnyTimes()
	q.EXPECT().GetLandlord(gomock.Any(), gomock.Any()).Return(sqlc.Landlord{ID: testLandlordID, Phone: "+254700000000"}, nil).AnyTimes()
	q.EXPECT().ListScheduleUnits(gomock.Any(), gomock.Any()).Return(scheduleUnits(), nil).AnyTimes()
	q.EXPECT().ListPeriodPayments(gomock.Any(), gomock.Any()).Return(payments, nil).AnyTimes()
	// Billed equals units x rate (3,000 = 3,000), so the blocking water
	// check always passes here; the tests that need a mismatch build
	// their own GetWaterSummary expectation instead of calling this.
	q.EXPECT().GetWaterSummary(gomock.Any(), gomock.Any()).Return(sqlc.GetWaterSummaryRow{
		UnitsConsumed: money("20"), Expected: money("3000"), Billed: money("3000"),
	}, nil).AnyTimes()
	q.EXPECT().GetReport(gomock.Any(), gomock.Any()).Return(sqlc.GetReportRow{Notes: json.RawMessage(notesJSON)}, nil).AnyTimes()
}

// expectConfirmationInputs mocks the confirmation gate's extra queries —
// the sanity check's pending-payments count and the NOTE:2 derivation —
// with no arrears/advances/commitments in play, so both Confirm's freeze
// and GenerateConfirmed's live re-check compute the same checksum.
func expectConfirmationInputs(q *mock.MockQuerier) {
	q.EXPECT().CountPendingPaymentsForPeriod(gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	q.EXPECT().ListUnitRentBalancesAsOf(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListArrearsCommitments(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListUnitLeaseRentHistory(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	// No storage capacity configured and no plot-meter reading recorded:
	// NOTE:1's storage/meter lines are omitted, not printed as zero.
	q.EXPECT().GetPropertyMeterReading(gomock.Any(), gomock.Any()).Return((*moneyfmt.Money)(nil), sql.ErrNoRows).AnyTimes()
}

// confirmForTest drives models.Reports.Confirm directly (not through HTTP)
// so a generate-handler test can seed a matching confirmation without
// duplicating the checksum logic: UpsertReportConfirmation is mocked to
// hand the same values straight back out of GetReportConfirmation.
func confirmForTest(t *testing.T, app *application, q *mock.MockQuerier) {
	t.Helper()
	var stored sqlc.GetReportConfirmationRow
	q.EXPECT().ArchiveReportConfirmation(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	q.EXPECT().UpsertReportConfirmation(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, p sqlc.UpsertReportConfirmationParams) (sqlc.UpsertReportConfirmationRow, error) {
			stored = sqlc.GetReportConfirmationRow{TotalsChecksum: p.TotalsChecksum, NoteLines: p.NoteLines, ConfirmedBy: p.ConfirmedBy}
			return sqlc.UpsertReportConfirmationRow{ID: uuid.New()}, nil
		})
	period, err := moneyfmt.ParsePeriod("2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.models.Reports.Confirm(context.Background(), testTenantID, testTenantID, testPropertyID, period); err != nil {
		t.Fatalf("confirmForTest: %v", err)
	}
	q.EXPECT().GetReportConfirmation(gomock.Any(), gomock.Any()).Return(stored, nil).AnyTimes()
}

func TestGenerateMonthlyReport(t *testing.T) {
	payments := []sqlc.ListPeriodPaymentsRow{
		payment(testUnitA, "RENT", 3, "5000.00", "payhero_stk"),
		payment(testUnitA, "RENT", 17, "2500.00", "manual"), // two payments, one cell
		payment(testUnitA, "WATER", 3, "1000.00", "payhero_stk"),
		payment(testUnitA, "GARBAGE", 3, "300.00", "payhero_stk"),
		payment(testUnitB, "RENT", 9, "8000.00", "payhero_stk"),
	}

	t.Run("unconfirmed period is a 409", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		expectReportInputs(q, false, payments, `[]`)
		q.EXPECT().GetReportConfirmation(gomock.Any(), gomock.Any()).Return(sqlc.GetReportConfirmationRow{}, sql.ErrNoRows)

		w := serveAsManager(app.generateMonthlyReportHandler, http.MethodPost, "/", "", propParams(periodParam))
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("garbage disabled: column absent, confirmed period serves the PDF", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		expectReportInputs(q, false, payments, `[]`)
		expectConfirmationInputs(q)
		confirmForTest(t, app, q)

		w := serveAsManager(app.generateMonthlyReportHandler, http.MethodPost, "/", "", propParams(periodParam))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/pdf" {
			t.Fatalf("status %d: %.300s", w.Code, w.Body)
		}
		body := w.Body.String()
		for _, want := range []string{
			"ALL IN ONE PAYMENTS SCHEDULE: MONTH: SEPTEMBER 2026",
			"JOHN KAMAU", "MARY WANJIKU", "PETER OMONDI", "OR",
			"3/9/2026", "17/9/2026", "5,000", "2,500", "SHOP", "NO.2",
			// Fee is 5% of rent collected (7,500 + 8,000 = 15,500), not
			// the grand total (which also includes the 1,000 water paid).
			"MANAGEMENT FEE: 5/100 X KSH. 15,500 = KSHS. 775",
			"HOUSES OCCUPIED: 2", "VACANT HOUSES: 1",
			// ActualWater is what tenants paid (the one WATER payment,
			// 1,000), not what was billed (3,000, matching expected so
			// the blocking check passes): deviation is -2,000.
			"PAID LESS KSH. 2,000",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("PDF missing %q", want)
			}
		}
		if strings.Contains(body, "GARBAGE") {
			t.Error("garbage column must be absent when garbage is disabled")
		}
	})

	t.Run("garbage enabled: column present and counted", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		expectReportInputs(q, true, payments, `[]`)
		expectConfirmationInputs(q)
		confirmForTest(t, app, q)

		w := serveAsManager(app.generateMonthlyReportHandler, http.MethodPost, "/", "", propParams(periodParam))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "GARBAGE PAID") {
			t.Fatalf("status %d", w.Code)
		}
	})

	t.Run("bad period is a 404", func(t *testing.T) {
		app, _ := newPropertyTestApp(t)
		w := serveAsManager(app.generateMonthlyReportHandler, http.MethodPost, "/", "", propParams(gin.Param{Key: "period", Value: "9-2026"}))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestShowMonthlyReport(t *testing.T) {
	app, q := newPropertyTestApp(t)
	expectReportInputs(q, false, []sqlc.ListPeriodPaymentsRow{payment(testUnitB, "RENT", 9, "8000.00", "payhero_stk")}, `["a note"]`)

	w := serveAsManager(app.showMonthlyReportHandler, http.MethodGet, "/", "", propParams(periodParam))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	for _, want := range []string{`"grand_total": "8000.00"`, `"management_fee": "400.00"`, `"a note"`, `"house_no": "G3"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body missing %q:\n%s", want, w.Body)
		}
	}
}

// receiptRow builds one ListReceiptsForPeriodRow fixture. Receipts are
// issued at posting time now (one per payment allocation); generating the
// PDF is a pure read of already-issued rows.
func receiptRow(no int64, unit uuid.UUID, unitCode, tenantName, ledgerType, amount, source string) sqlc.ListReceiptsForPeriodRow {
	lt := ledgerType
	return sqlc.ListReceiptsForPeriodRow{
		ID: uuid.New(), TenantID: testTenantID, UnitID: unit, ReceiptNo: no,
		Period: day(1), GeneratedAt: day(5), PropertyID: testPropertyID,
		LedgerType: &lt, UnitCode: unitCode, TenantName: tenantName,
		MpesaReceipt: "QWE" + strconv.FormatInt(no, 10), Source: source, Amount: money(amount),
	}
}

func TestDownloadReceiptsPDF(t *testing.T) {
	setup := func(q *mock.MockQuerier, rows []sqlc.ListReceiptsForPeriodRow) {
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		q.EXPECT().GetLandlord(gomock.Any(), gomock.Any()).Return(testLandlordRowWithBank(), nil)
		q.EXPECT().ListReceiptsForPeriod(gomock.Any(), gomock.Any()).Return(rows, nil)
		// Receipts aren't gated on confirmation; this period simply isn't
		// confirmed, so no stamp is added. Not reached when none were issued.
		q.EXPECT().GetReportConfirmation(gomock.Any(), gomock.Any()).Return(sqlc.GetReportConfirmationRow{}, sql.ErrNoRows).AnyTimes()
	}

	t.Run("one receipt per allocation, own numbers, own ledger type", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setup(q, []sqlc.ListReceiptsForPeriodRow{
			receiptRow(1042, testUnitA, "G1", "JOHN KAMAU", "RENT", "5000.00", "payhero_stk"),
			receiptRow(1043, testUnitA, "G1", "JOHN KAMAU", "RENT_DEPOSIT", "5000.00", "payhero_stk"),
			receiptRow(1044, testUnitB, "SHOP NO.2", "ACME LTD", "RENT", "8000.00", "manual"),
		})

		w := serveAsManager(app.showReceiptsPDFHandler, http.MethodGet, "/", "", propParams(periodParam))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "%PDF") {
			t.Fatalf("status %d: %.300s", w.Code, w.Body)
		}
		for _, want := range []string{"1042", "1043", "1044", "JOHN KAMAU", "ACME LTD", "5,000", "8,000", "QWE1042"} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("PDF missing %q", want)
			}
		}
	})

	t.Run("nobody paid: nothing issued this period", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setup(q, nil)
		w := serveAsManager(app.showReceiptsPDFHandler, http.MethodGet, "/", "", propParams(periodParam))
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("unit filter narrows to one unit's receipts", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setup(q, []sqlc.ListReceiptsForPeriodRow{
			receiptRow(7, testUnitB, "SHOP NO.2", "ACME LTD", "RENT", "8000.00", "payhero_stk"),
		})
		w := serveAsManager(app.showReceiptsPDFHandler, http.MethodGet, "/?unit_id="+testUnitB.String(), "", propParams(periodParam))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "JOHN KAMAU") {
			t.Fatalf("status %d, must contain only ACME LTD's receipt", w.Code)
		}
	})
}

// TestShowPlotMeterReading is the 2026-09-28 regression: a property/period
// with no plot meter reading recorded at all (and none before it either)
// must come back 200 with every field null, never a 500 from treating
// sql.ErrNoRows as an error.
func TestShowPlotMeterReading(t *testing.T) {
	t.Run("never read: 200 with null fields", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		q.EXPECT().GetPlotMeterReadingRow(gomock.Any(), gomock.Any()).Return(sqlc.GetPlotMeterReadingRowRow{}, sql.ErrNoRows)
		q.EXPECT().GetPreviousPropertyMeterReading(gomock.Any(), gomock.Any()).Return(moneyfmt.Money{}, sql.ErrNoRows)

		w := serveAsManager(app.showPlotMeterReadingHandler, http.MethodGet, "/", "", propParams(periodParam))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", w.Code, w.Body)
		}
		var body struct {
			PlotMeter struct {
				Previous *string `json:"previous_reading"`
				Current  *string `json:"current_reading"`
				Units    *string `json:"units_consumed"`
			} `json:"plot_meter"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.PlotMeter.Previous != nil || body.PlotMeter.Current != nil || body.PlotMeter.Units != nil {
			t.Errorf("plot_meter = %+v, want every field null", body.PlotMeter)
		}
	})

	t.Run("no reading this period, but one on file from last period", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		q.EXPECT().GetPlotMeterReadingRow(gomock.Any(), gomock.Any()).Return(sqlc.GetPlotMeterReadingRowRow{}, sql.ErrNoRows)
		q.EXPECT().GetPreviousPropertyMeterReading(gomock.Any(), gomock.Any()).Return(money("500"), nil)

		w := serveAsManager(app.showPlotMeterReadingHandler, http.MethodGet, "/", "", propParams(periodParam))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"previous_reading": "500.00"`) {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
}
