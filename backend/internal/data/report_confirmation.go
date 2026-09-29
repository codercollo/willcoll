package data

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

var (
	// ErrPeriodNotConfirmed is returned when a PDF is requested for a
	// period that has never been confirmed.
	ErrPeriodNotConfirmed = errors.New("period is not confirmed")

	// ErrPeriodStale is returned when a PDF is requested for a period
	// whose confirmed totals no longer match the live ledger: something
	// posted after confirmation (a new payment, a reversal, an edited
	// reading) and it must be re-confirmed before any PDF is served.
	ErrPeriodStale = errors.New("confirmed period is stale; the ledger changed since confirmation")
)

// SanityFailure is one blocking reason a period cannot be confirmed.
type SanityFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SanityResult is the confirmation gate's outcome. Failures block Confirm;
// Info is shown in the review panel but never blocks (system-design.txt
// PDF confirmation addendum: water paid-vs-billed is informational, water
// billed-vs-units×rate is blocking).
type SanityResult struct {
	Failures []SanityFailure `json:"failures"`
	Info     []string        `json:"info"`
}

// Passed reports whether the period may be confirmed.
func (r SanityResult) Passed() bool { return len(r.Failures) == 0 }

// SanityCheckError is returned by Confirm when the period fails a blocking
// check; Result carries the failures (and info) so the caller doesn't have
// to re-run Checks to show them.
type SanityCheckError struct {
	Result *SanityResult
}

func (e *SanityCheckError) Error() string { return "period failed sanity checks" }

// ReportNoteLines is the frozen NOTE:1 / NOTE:2 text a confirmation snapshots.
// A confirmed PDF renders these verbatim; nothing is re-derived from the
// live ledger once confirmed (that's the point of freezing at confirmation
// — later payments can change the FIFO order retroactively).
type ReportNoteLines struct {
	Note1 []string `json:"note1"`
	Note2 []string `json:"note2"`
}

// maxCommitmentNoteSize bounds a commitment-letter note (ValidateReportNotes
// uses the same 500-byte limit for the now-superseded manager-typed lines).
const maxCommitmentNoteSize = 500

// CreateArrearsCommitment records a commitment letter for a unit's arrears
// this period — the one manual input NOTE:2 still takes, since it is a
// manager's record of a real paper document, not derivable from the
// ledger.
func (m ReportModel) CreateArrearsCommitment(ctx context.Context, tenantID, createdBy, unitID uuid.UUID, period moneyfmt.Period, note string) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	note = strings.TrimSpace(note)
	if note == "" {
		return fmt.Errorf("%w: note must not be empty", ErrInvalidInput)
	}
	if len(note) > maxCommitmentNoteSize {
		return fmt.Errorf("%w: note must not be more than 500 bytes long", ErrInvalidInput)
	}

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}
		_, err := q.CreateArrearsCommitment(ctx, sqlc.CreateArrearsCommitmentParams{
			TenantID: tenantID, UnitID: unitID, Period: period.FirstDay(), Note: note, CreatedBy: createdBy,
		})
		return err
	})
}

// PlotMeterReading is the reading form's state for one period: the dial
// readings and the units they imply, mirroring a unit's water reading.
// Previous is nil only for a property never read before (or the first
// period after this feature shipped) — editable in that case, same as a
// brand-new unit meter.
type PlotMeterReading struct {
	Previous      *moneyfmt.Money `json:"previous_reading"`
	Current       *moneyfmt.Money `json:"current_reading"`
	UnitsConsumed *moneyfmt.Money `json:"units_consumed"`
	ReadingDate   *time.Time      `json:"reading_date"`
}

// defaultPreviousPlotMeterReading is the last period before this one that
// was read, or nil if the property has never been read before.
func defaultPreviousPlotMeterReading(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*moneyfmt.Money, error) {
	prev, err := q.GetPreviousPropertyMeterReading(ctx, sqlc.GetPreviousPropertyMeterReadingParams{
		TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	switch {
	case err == nil:
		return &prev, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, err
	}
}

// PlotMeterGrid returns the period's plot meter reading state for the form:
// "Previous: X  Current: [ ]  = Y units used".
func (m ReportModel) PlotMeterGrid(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*PlotMeterReading, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var reading PlotMeterReading
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID}); err != nil {
			return notFound(err)
		}
		row, err := q.GetPlotMeterReadingRow(ctx, sqlc.GetPlotMeterReadingRowParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		switch {
		case err == nil:
			cur := row.CurrentReading
			reading = PlotMeterReading{Previous: row.PreviousReading, Current: &cur, UnitsConsumed: row.UnitsConsumed, ReadingDate: &row.ReadingDate}
		case errors.Is(err, sql.ErrNoRows):
			// Nothing saved this period yet: Current/UnitsConsumed/ReadingDate stay nil.
		default:
			return err
		}
		if reading.Previous == nil {
			prev, err := defaultPreviousPlotMeterReading(ctx, q, tenantID, propertyID, period)
			if err != nil {
				return err
			}
			reading.Previous = prev
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &reading, nil
}

// SetPlotMeterReading records the property's plot meter dial reading for
// the period, the same shape as a unit's water reading: previousOverride
// defaults to last period's saved current reading (nil for a property
// never read before, which the caller must then supply explicitly — the
// first month is editable). Rejects a current reading below the previous
// one. Not calling this simply leaves NOTE:1's plot-meter lines omitted —
// never printed as zero.
func (m ReportModel) SetPlotMeterReading(ctx context.Context, tenantID, recordedBy, propertyID uuid.UUID, period moneyfmt.Period, current moneyfmt.Money, previousOverride *moneyfmt.Money, readingDate time.Time) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if current.IsNegative() {
		return fmt.Errorf("%w: current reading must not be negative", ErrInvalidInput)
	}
	if previousOverride != nil && previousOverride.IsNegative() {
		return fmt.Errorf("%w: previous reading must not be negative", ErrInvalidInput)
	}
	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID}); err != nil {
			return notFound(err)
		}
		previous := previousOverride
		if previous == nil {
			// Preserve a previous reading already saved for this period
			// (e.g. a manager-supplied first-month figure with no prior
			// period to fall back to) rather than silently resetting it.
			existing, err := q.GetPlotMeterReadingRow(ctx, sqlc.GetPlotMeterReadingRowParams{
				TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
			})
			switch {
			case err == nil:
				previous = existing.PreviousReading
			case errors.Is(err, sql.ErrNoRows):
			default:
				return err
			}
		}
		if previous == nil {
			var err error
			previous, err = defaultPreviousPlotMeterReading(ctx, q, tenantID, propertyID, period)
			if err != nil {
				return err
			}
		}
		if previous != nil && current.Cmp(*previous) < 0 {
			return fmt.Errorf("%w: current reading is below the previous reading", ErrInvalidReading)
		}
		if readingDate.IsZero() {
			readingDate = time.Now()
		}
		return q.UpsertPropertyMeterReading(ctx, sqlc.UpsertPropertyMeterReadingParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
			PreviousReading: previous, CurrentReading: current, RecordedBy: recordedBy, ReadingDate: readingDate,
		})
	})
}

// Checks runs every sanity check for the period without confirming
// anything, for the manager's review panel (GET .../reports/:period/checks).
func (m ReportModel) Checks(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*SanityResult, *MonthlyReport, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var (
		result *SanityResult
		report *MonthlyReport
	)
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		report, err = buildReport(ctx, q, tenantID, propertyID, period)
		if err != nil {
			return err
		}
		result, err = sanityCheck(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		// A live preview of NOTE:2, so the review panel shows what
		// Confirm would freeze, not the now-superseded manager-typed
		// lines buildReport otherwise fills in.
		note2, err := deriveNote2(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		note1, err := buildNote1Lines(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		report.Note1, report.Notes = note1, note2

		confirmation, err := q.GetReportConfirmation(ctx, sqlc.GetReportConfirmationParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		switch {
		case err == nil:
			report.Confirmed = true
			report.ConfirmedAt = confirmation.ConfirmedAt
			live := snapshotChecksum(report.Totals, ReportNoteLines{Note1: note1, Note2: note2})
			report.Stale = live != confirmation.TotalsChecksum
		case errors.Is(err, sql.ErrNoRows):
			// Never confirmed: Confirmed/Stale stay at their zero values.
		default:
			return err
		}
		return nil
	})
	return result, report, err
}

// Confirm runs the sanity checks, and on success freezes the report's
// totals, NOTE:1 and NOTE:2 into report_confirmations under a checksum of
// the totals. Returns *SanityCheckError if a blocking check fails.
func (m ReportModel) Confirm(ctx context.Context, tenantID, confirmedBy, propertyID uuid.UUID, period moneyfmt.Period) (*MonthlyReport, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var report *MonthlyReport
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		report, err = buildReport(ctx, q, tenantID, propertyID, period)
		if err != nil {
			return err
		}

		result, err := sanityCheck(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		if !result.Passed() {
			return &SanityCheckError{Result: result}
		}

		note2, err := deriveNote2(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		note1, err := buildNote1Lines(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		noteLines := ReportNoteLines{Note1: note1, Note2: note2}
		report.Note1, report.Notes = noteLines.Note1, noteLines.Note2

		rawNotes, err := json.Marshal(noteLines)
		if err != nil {
			return err
		}
		// The checksum covers the notes too, not just the totals: a
		// charge with no matching payment (a manual late fee) leaves
		// every printed total unchanged but shifts the arrears/advance
		// balance NOTE:2 is derived from, and must still go stale.
		checksum := snapshotChecksum(report.Totals, noteLines)

		// Preserve the superseded snapshot before the upsert below
		// overwrites it, so a stale re-confirm doesn't lose the prior
		// confirmation's record.
		if err := q.ArchiveReportConfirmation(ctx, sqlc.ArchiveReportConfirmationParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		}); err != nil {
			return err
		}

		confirmation, err := q.UpsertReportConfirmation(ctx, sqlc.UpsertReportConfirmationParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
			TotalsChecksum: checksum, NoteLines: rawNotes, ConfirmedBy: confirmedBy,
		})
		if err != nil {
			return err
		}
		report.ConfirmedAt = confirmation.ConfirmedAt
		return nil
	})
	return report, err
}

// GenerateConfirmed builds the report and checks it against its
// confirmation snapshot: ErrPeriodNotConfirmed if the period was never
// confirmed, ErrPeriodStale if the live totals no longer match the
// checksum. On success the report's Notes are replaced with the frozen
// NOTE:2 lines, so the PDF renders the snapshot, not a live re-derivation.
func (m ReportModel) GenerateConfirmed(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*MonthlyReport, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var report *MonthlyReport
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		report, err = buildReport(ctx, q, tenantID, propertyID, period)
		if err != nil {
			return err
		}

		confirmation, err := q.GetReportConfirmation(ctx, sqlc.GetReportConfirmationParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPeriodNotConfirmed
		}
		if err != nil {
			return err
		}
		var noteLines ReportNoteLines
		if err := json.Unmarshal(confirmation.NoteLines, &noteLines); err != nil {
			return err
		}

		// Recompute live to detect drift the printed totals alone would
		// miss (a charge with no matching payment still shifts NOTE:2).
		if _, err := sanityCheck(ctx, q, tenantID, propertyID, period, report); err != nil {
			return err
		}
		liveNote2, err := deriveNote2(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		liveNote1, err := buildNote1Lines(ctx, q, tenantID, propertyID, period, report)
		if err != nil {
			return err
		}
		liveNoteLines := ReportNoteLines{Note1: liveNote1, Note2: liveNote2}
		if snapshotChecksum(report.Totals, liveNoteLines) != confirmation.TotalsChecksum {
			return ErrPeriodStale
		}

		report.Note1, report.Notes = noteLines.Note1, noteLines.Note2
		report.ConfirmedAt = confirmation.ConfirmedAt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// snapshotChecksum hashes the totals and the derived note lines together:
// both have a fixed field layout, so the JSON encoding is stable.
func snapshotChecksum(t ReportTotals, notes ReportNoteLines) string {
	raw, _ := json.Marshal(struct {
		Totals ReportTotals    `json:"totals"`
		Notes  ReportNoteLines `json:"notes"`
	}{t, notes})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sanityCheck(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID, period moneyfmt.Period, report *MonthlyReport) (*SanityResult, error) {
	result := &SanityResult{}

	// Row totals equal the ledger sum.
	var rowSum moneyfmt.Money
	for _, row := range report.Rows {
		rowSum = rowSum.Add(row.Total(report.GarbageEnabled, report.ElectricityDepositShown))
	}
	if rowSum.Cmp(report.Totals.Grand) != 0 {
		result.Failures = append(result.Failures, SanityFailure{
			Code:    "row_totals_mismatch",
			Message: fmt.Sprintf("row totals sum to KSH. %s, the ledger grand total is KSH. %s", rowSum.Display(), report.Totals.Grand.Display()),
		})
	}

	// Column totals equal the grand total.
	colSum := report.Totals.Rent.Add(report.Totals.Water).Add(report.Totals.RentDeposit).Add(report.Totals.WaterDeposit)
	if report.GarbageEnabled {
		colSum = colSum.Add(report.Totals.Garbage)
	}
	if report.ElectricityDepositShown {
		colSum = colSum.Add(report.Totals.ElectricityDeposit)
	}
	if colSum.Cmp(report.Totals.Grand) != 0 {
		result.Failures = append(result.Failures, SanityFailure{
			Code:    "column_totals_mismatch",
			Message: fmt.Sprintf("column totals sum to KSH. %s, the grand total is KSH. %s", colSum.Display(), report.Totals.Grand.Display()),
		})
	}

	// Water billed equals units x rate (billing integrity: every reading
	// must have been charged at the rate this recomputes, not a stale
	// rate_snapshot). Water paid vs. billed is informational only — it is
	// the NOTE:1 deviation, a real-world collections shortfall, not a
	// data error, so it never blocks.
	water, err := q.GetWaterSummary(ctx, sqlc.GetWaterSummaryParams{
		Rate: report.Summary.WaterRate, TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	if err != nil {
		return nil, err
	}
	if water.Expected.Cmp(water.Billed) != 0 {
		result.Failures = append(result.Failures, SanityFailure{
			Code:    "water_billed_mismatch",
			Message: fmt.Sprintf("water billed KSH. %s does not equal units x rate KSH. %s; a reading may have used a stale rate", water.Billed.Display(), water.Expected.Display()),
		})
	}
	if !report.Deviation.IsZero() {
		if report.Deviation.IsNegative() {
			result.Info = append(result.Info, fmt.Sprintf("water: tenants paid KSH. %s less than billed", report.Deviation.Neg().Display()))
		} else {
			result.Info = append(result.Info, fmt.Sprintf("water: tenants paid KSH. %s more than billed", report.Deviation.Display()))
		}
	}

	// Plot meter reading sanity: informational only, since a wrong reading
	// is a data-entry slip, not a ledger error, and must not block Confirm.
	// Flags a reading that looks like a currency amount typed into the
	// units field (implausibly low against what tenants were billed, or
	// implausibly high, e.g. off by a factor of the water rate).
	meter, err := q.GetPropertyMeterReading(ctx, sqlc.GetPropertyMeterReadingParams{
		TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	switch {
	case err == nil:
		if !report.Summary.WaterUnits.IsZero() {
			if meter.Cmp(report.Summary.WaterUnits) < 0 {
				result.Info = append(result.Info, fmt.Sprintf(
					"plot meter reading (%s units) is below the %s units billed to tenants; check the plot meter reading",
					displayWhole(*meter), displayWhole(report.Summary.WaterUnits)))
			} else if triple := report.Summary.WaterUnits.Add(report.Summary.WaterUnits).Add(report.Summary.WaterUnits); meter.Cmp(triple) > 0 {
				result.Info = append(result.Info, fmt.Sprintf(
					"plot meter reading (%s units) is more than 3x the %s units billed to tenants; check the plot meter reading",
					displayWhole(*meter), displayWhole(report.Summary.WaterUnits)))
			}
		}
	case errors.Is(err, sql.ErrNoRows):
		// Not recorded this period: nothing to sanity-check.
	default:
		return nil, err
	}

	// No unallocated or review-queue payments left for the period.
	pending, err := q.CountPendingPaymentsForPeriod(ctx, sqlc.CountPendingPaymentsForPeriodParams{
		TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	if err != nil {
		return nil, err
	}
	if pending > 0 {
		result.Failures = append(result.Failures, SanityFailure{
			Code:    "pending_payments",
			Message: fmt.Sprintf("%d payment(s) for this property's period are unallocated or awaiting review", pending),
		})
	}

	// Occupied + vacant equals the total number of units.
	if report.Summary.Occupied+report.Summary.Vacant != len(report.Rows) {
		result.Failures = append(result.Failures, SanityFailure{
			Code:    "unit_count_mismatch",
			Message: "occupied + vacant does not equal the total number of units",
		})
	}

	return result, nil
}

// buildNote1Lines renders NOTE:1 in the Rundas wording. Storage-capacity
// and plot-meter lines are omitted entirely until a property records its
// storage configuration and a plot-meter reading for the period — never
// printed as zero.
func buildNote1Lines(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID, period moneyfmt.Period, report *MonthlyReport) ([]string, error) {
	sm := report.Summary
	lines := []string{
		fmt.Sprintf("A) HOUSES OCCUPIED: %d", sm.Occupied),
		fmt.Sprintf("B) VACANT HOUSES: %d", sm.Vacant),
		fmt.Sprintf("-    TOTAL WATER UNITS CONSUMED BY TENANTS: %s", displayWhole(sm.WaterUnits)),
		fmt.Sprintf("-    WATER PAYMENT RATE PER UNIT: KSH. %s", displayWhole(sm.WaterRate)),
		fmt.Sprintf("-    EXPECTED TOTAL WATER BILLS PAYMENT BY TENANTS: KSH. %s", displayWhole(sm.ExpectedWater)),
		fmt.Sprintf("-    TOTAL WATER BILLS PAYMENT: KSH. %s", displayWhole(sm.ActualWater)),
	}
	switch {
	case report.Deviation.IsZero():
		lines = append(lines, "-    THE DEVIATION/ THE DIFFERENCE OF WATER BILLED AND WATER PAID: NONE")
	case report.Deviation.IsNegative():
		lines = append(lines, fmt.Sprintf("-    THE DEVIATION/ THE DIFFERENCE OF WATER BILLED AND WATER PAID: - (VE) MEANING, PAID LESS KSH. %s", displayWhole(report.Deviation.Neg())))
	default:
		lines = append(lines, fmt.Sprintf("-    THE DEVIATION/ THE DIFFERENCE OF WATER BILLED AND WATER PAID: + (VE) MEANING, PAID MORE KSH. %s", displayWhole(report.Deviation)))
	}

	property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
	if err != nil {
		return nil, err
	}
	var storageTotal *moneyfmt.Money
	if property.UndergroundCapacityUnits != nil {
		lines = append(lines, fmt.Sprintf("-    UNDERGROUND WATER STORAGE CAPACITY IN UNITS: %s", displayWhole(*property.UndergroundCapacityUnits)))
		storageTotal = property.UndergroundCapacityUnits
	}
	if property.RooftopCapacityUnits != nil {
		lines = append(lines, fmt.Sprintf("-    ROOFTOP WATER TANKS CAPACITY IN UNITS: %s", displayWhole(*property.RooftopCapacityUnits)))
		if storageTotal == nil {
			storageTotal = property.RooftopCapacityUnits
		} else {
			t := storageTotal.Add(*property.RooftopCapacityUnits)
			storageTotal = &t
		}
	}
	if property.UndergroundCapacityUnits != nil && property.RooftopCapacityUnits != nil {
		lines = append(lines, fmt.Sprintf("-    TOTAL WATER STORAGE IN UNITS: %s", displayWhole(*storageTotal)))
	}

	meter, err := q.GetPropertyMeterReading(ctx, sqlc.GetPropertyMeterReadingParams{
		TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	switch {
	case err == nil:
		lines = append(lines, fmt.Sprintf("-    TOTAL WATER PASSED THROUGH THE PLOT METER IN UNITS: %s", displayWhole(*meter)))
		if storageTotal != nil {
			lines = append(lines, fmt.Sprintf("-    BOTH TOTALS OF WATER BILLED AND THE WATER IN STORAGE IN UNITS: %s", displayWhole(*meter)))
		}
		diff := meter.Sub(sm.WaterUnits)
		lines = append(lines, fmt.Sprintf("-    THE DEVIATION/ THE DIFFERENCE IN UNITS OF WATER IN THE STORAGE AND THE WATER BILLED FOR TENANTS: %s-%s = %s",
			displayWhole(*meter), displayWhole(sm.WaterUnits), displayWhole(diff)))
	case errors.Is(err, sql.ErrNoRows):
		// Not recorded this period: omit the plot-meter lines entirely.
	default:
		return nil, err
	}

	return lines, nil
}

// displayWhole trims a money value's ".00" — Rundas amounts print as whole
// shillings ("24,300"), never with cents, for round figures.
func displayWhole(m moneyfmt.Money) string {
	return strings.TrimSuffix(m.Display(), ".00")
}

// deriveNote2 computes the advance/arrears exception lines straight from
// the ledger: no free-text manager input except commitment letters
// (arrears_commitments), which are a manager's record of a real paper
// document, not a derivable fact.
func deriveNote2(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID, period moneyfmt.Period, report *MonthlyReport) ([]string, error) {
	units, err := q.ListScheduleUnits(ctx, sqlc.ListScheduleUnitsParams{
		Period: period.FirstDay(), TenantID: tenantID, PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}

	periodStart := period.FirstDay()
	periodEnd := period.Next().FirstDay()

	prevBalances, err := q.ListUnitRentBalancesAsOf(ctx, sqlc.ListUnitRentBalancesAsOfParams{
		AsOf: periodStart, TenantID: tenantID, PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	curBalances, err := q.ListUnitRentBalancesAsOf(ctx, sqlc.ListUnitRentBalancesAsOfParams{
		AsOf: periodEnd, TenantID: tenantID, PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	prevByUnit := make(map[uuid.UUID]moneyfmt.Money, len(prevBalances))
	for _, b := range prevBalances {
		prevByUnit[b.UnitID] = b.Balance
	}

	commitments, err := q.ListArrearsCommitments(ctx, sqlc.ListArrearsCommitmentsParams{
		TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	if err != nil {
		return nil, err
	}
	commitmentByUnit := make(map[uuid.UUID][]string, len(commitments))
	for _, c := range commitments {
		commitmentByUnit[c.UnitID] = append(commitmentByUnit[c.UnitID], c.Note)
	}

	unitCode := make(map[uuid.UUID]string, len(units))
	tenantName := make(map[uuid.UUID]string, len(units))
	for _, u := range units {
		unitCode[u.UnitID] = u.UnitCode
		tenantName[u.UnitID] = u.TenantName
	}

	unitIDs := make([]uuid.UUID, 0, len(units))
	for _, u := range units {
		unitIDs = append(unitIDs, u.UnitID)
	}
	leaseHistory, err := q.ListUnitLeaseRentHistory(ctx, sqlc.ListUnitLeaseRentHistoryParams{
		TenantID: tenantID, UnitIds: unitIDs,
	})
	if err != nil {
		return nil, err
	}
	rentByUnit := make(map[uuid.UUID][]sqlc.ListUnitLeaseRentHistoryRow)
	for _, l := range leaseHistory {
		rentByUnit[l.UnitID] = append(rentByUnit[l.UnitID], l)
	}

	var lines []string
	for _, b := range curBalances {
		name := tenantName[b.UnitID]
		code := unitCode[b.UnitID]
		if name == "" {
			continue // vacant unit, or a lease we have no name for
		}
		prev := prevByUnit[b.UnitID]
		cur := b.Balance

		switch {
		case prev.IsPositive() && !cur.IsPositive():
			lines = append(lines, fmt.Sprintf("%s, HSE NO. %s. %s RENT ARREARS OF KSH. %s CLEARED.",
				name, code, strings.ToUpper(period.FirstDay().Format("JANUARY 2006")), displayWhole(prev)))
		case cur.IsPositive():
			var line string
			if months := arrearsMonths(period, cur, rentByUnit[b.UnitID]); len(months) > 1 {
				fromMonth := strings.Fields(months[0])[0]
				line = fmt.Sprintf("%s, HSE NO. %s. HAS RENT ARREARS OF KSH. %s FOR %s - %s.",
					name, code, displayWhole(cur), fromMonth, months[len(months)-1])
			} else {
				label := strings.ToUpper(period.FirstDay().Format("JANUARY 2006"))
				if len(months) == 1 {
					label = months[0]
				}
				line = fmt.Sprintf("%s, HSE NO. %s. HAS %s RENT ARREARS OF KSH. %s.", name, code, label, displayWhole(cur))
			}
			for _, note := range commitmentByUnit[b.UnitID] {
				line += " " + strings.ToUpper(note)
			}
			lines = append(lines, line)
		}

		if cur.IsNegative() {
			if advance := advanceNote(name, code, period, cur.Neg(), rentByUnit[b.UnitID]); advance != "" {
				lines = append(lines, advance)
			}
		}
	}

	// A commitment on a unit whose balance this period is exactly zero
	// (arrears cleared this month but the letter was recorded earlier)
	// still needs to print; the loop above only reaches units with a
	// nonzero current or previous balance path already covered above,
	// but guard the rare case of an untouched commitment note.
	for unitID, notes := range commitmentByUnit {
		if _, matched := prevByUnit[unitID]; matched {
			continue
		}
		for _, note := range notes {
			lines = append(lines, fmt.Sprintf("%s, HSE NO. %s. %s", tenantName[unitID], unitCode[unitID], strings.ToUpper(note)))
		}
	}

	sort.Strings(lines)
	return lines, nil
}

// arrearsMonths FIFO-derives which past months an outstanding rent balance
// covers, using the currently active lease's rent (the rent in force as of
// the period, the same simplification advanceNote makes for future months —
// exact per-month rent history isn't tracked separately from the ledger
// balance itself). Returns oldest-to-newest periods, one per month of
// arrears (a partially paid month still counts as one owed month). Empty
// if there's no lease to price a month against.
func arrearsPeriods(period moneyfmt.Period, arrears moneyfmt.Money, leases []sqlc.ListUnitLeaseRentHistoryRow) []moneyfmt.Period {
	if len(leases) == 0 || !arrears.IsPositive() {
		return nil
	}
	active := leases[len(leases)-1]
	rent := active.RentAmount
	if rent.Cents() <= 0 {
		return nil
	}

	months := arrears.Cents() / rent.Cents()
	if arrears.Cents()%rent.Cents() != 0 {
		months++
	}
	if months == 0 {
		months = 1
	}

	periods := make([]moneyfmt.Period, months)
	cursor := period
	for i := months - 1; i >= 0; i-- {
		periods[i] = cursor
		cursor = cursor.Prev()
	}
	return periods
}

// arrearsMonths is arrearsPeriods formatted for NOTE:2's "MONTH YYYY" lines.
func arrearsMonths(period moneyfmt.Period, arrears moneyfmt.Money, leases []sqlc.ListUnitLeaseRentHistoryRow) []string {
	periods := arrearsPeriods(period, arrears, leases)
	if periods == nil {
		return nil
	}
	labels := make([]string, len(periods))
	for i, p := range periods {
		labels[i] = strings.ToUpper(p.FirstDay().Format("January 2006"))
	}
	return labels
}

// advanceNote FIFO-derives which future months a credit balance covers,
// using the rent that will be in force for each month (the currently
// active lease's rent for months after the period, since a future lease
// change has, by definition, not happened yet).
func advanceNote(name, code string, period moneyfmt.Period, credit moneyfmt.Money, leases []sqlc.ListUnitLeaseRentHistoryRow) string {
	if len(leases) == 0 || !credit.IsPositive() {
		return ""
	}
	// The lease active at the end of the confirmed period.
	active := leases[len(leases)-1]
	rent := active.RentAmount
	if rent.Cents() <= 0 {
		return ""
	}

	months := credit.Cents() / rent.Cents()
	remainder := credit.Cents() % rent.Cents()
	if months == 0 {
		return "" // a partial-month credit smaller than one rent isn't an "advance", just a small overpayment
	}

	names := make([]string, 0, months)
	cursor := period
	for i := int64(0); i < months; i++ {
		cursor = cursor.Next()
		names = append(names, strings.ToUpper(cursor.FirstDay().Format("January 2006")))
	}

	var monthsClause string
	switch len(names) {
	case 1:
		monthsClause = names[0]
	case 2:
		monthsClause = names[0] + " AND " + names[1]
	default:
		monthsClause = strings.Join(names[:len(names)-1], ", ") + " AND " + names[len(names)-1]
	}

	whole := credit.Sub(moneyfmt.FromCents(remainder))
	var line string
	if len(names) == 1 {
		line = fmt.Sprintf("%s, HSE NO. %s. %s FULL RENT OF KSH. %s PAID IN ADVANCE.",
			name, code, names[0], displayWhole(rent))
	} else {
		line = fmt.Sprintf("%s, HSE NO. %s. RENT PAID IN ADVANCE FOR %s, AMOUNTING TO KSH. %s.",
			name, code, monthsClause, displayWhole(whole))
	}
	if remainder > 0 {
		line += fmt.Sprintf(" PLUS A PARTIAL PAYMENT OF KSH. %s TOWARDS %s.",
			displayWhole(moneyfmt.FromCents(remainder)), strings.ToUpper(cursor.Next().FirstDay().Format("January 2006")))
	}
	return line
}
