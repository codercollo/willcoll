package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// ReportTotals are the column totals of the payments schedule.
type ReportTotals struct {
	Rent               moneyfmt.Money `json:"rent"`
	Water              moneyfmt.Money `json:"water"`
	Garbage            moneyfmt.Money `json:"garbage"`
	RentDeposit        moneyfmt.Money `json:"rent_deposit"`
	WaterDeposit       moneyfmt.Money `json:"water_deposit"`
	ElectricityDeposit moneyfmt.Money `json:"electricity_deposit"`
	Grand              moneyfmt.Money `json:"grand_total"`
}

// MonthlyReport is the "ALL IN ONE PAYMENTS SCHEDULE" for one property and
// period, derived from the ledger on every request.
type MonthlyReport struct {
	Period         moneyfmt.Period `json:"period"`
	PropertyName   string          `json:"property_name"`
	Location       string          `json:"location"`
	GarbageEnabled bool            `json:"garbage_enabled"`
	// ElectricityDepositShown is true when the property currently has
	// electricity enabled, OR any electricity deposit was paid this period
	// — so a period's column doesn't vanish from its own history just
	// because the property later switches electricity off.
	ElectricityDepositShown bool                `json:"electricity_deposit_shown"`
	Summary                 pdf.ScheduleSummary `json:"summary"`
	Deviation               moneyfmt.Money      `json:"deviation"`
	Rows                    []pdf.ScheduleRow   `json:"rows"`
	Totals                  ReportTotals        `json:"totals"`
	// Note1 / Notes (NOTE:2) are only populated by Checks (a live preview)
	// or Confirm/GenerateConfirmed (the frozen snapshot) — Preview leaves
	// them empty, since NOTE:1/NOTE:2 only exist once a period is at
	// least being reviewed for confirmation.
	Note1                []string       `json:"note1"`
	Notes                []string       `json:"notes"`
	ManagementFeePercent float64        `json:"management_fee_percent"`
	ManagementFee        moneyfmt.Money `json:"management_fee"`
	// ConfirmedAt is set by Confirm/GenerateConfirmed; zero otherwise.
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
	// Confirmed/Stale are populated by Checks: whether the period has a
	// confirmation at all, and whether the live totals/notes have since
	// drifted from that confirmation's checksum (the frontend's status
	// badge and the download buttons' 409 handling both key off this
	// instead of re-deriving it themselves).
	Confirmed bool `json:"confirmed"`
	Stale     bool `json:"stale"`

	theme pdf.Theme
}

// Schedule converts the report to the PDF builder's input. r.ConfirmedAt
// prints next to the management fee line (system-design.txt PDF
// confirmation addendum: "with the posting date").
func (r *MonthlyReport) Schedule() pdf.MonthlySchedule {
	return pdf.MonthlySchedule{
		Theme: r.theme, PropertyName: r.PropertyName, Location: r.Location,
		Period: r.Period.FirstDay(), GarbageEnabled: r.GarbageEnabled,
		ElectricityDepositShown: r.ElectricityDepositShown,
		Note1:                   r.Note1, Rows: r.Rows, Notes: r.Notes,
		ManagementFee: pdf.ManagementFee{
			Percent: strconv.FormatFloat(r.ManagementFeePercent, 'f', -1, 64),
			Fee:     r.ManagementFee,
		},
		ConfirmedAt: r.ConfirmedAt,
	}
}

// ReportModel is the service layer for monthly reports.
type ReportModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Preview returns the period's report (the JSON behind GET /reports/:period).
func (m ReportModel) Preview(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*MonthlyReport, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var report *MonthlyReport
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		report, err = buildReport(ctx, q, tenantID, propertyID, period)
		return err
	})
	return report, err
}

// Generate builds the report and records its totals snapshot. The PDF is
// then rendered from the returned report; nothing is stored beyond totals.
func (m ReportModel) Generate(ctx context.Context, tenantID, generatedBy, propertyID uuid.UUID, period moneyfmt.Period) (*MonthlyReport, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var report *MonthlyReport
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		report, err = buildReport(ctx, q, tenantID, propertyID, period)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(report.Totals)
		if err != nil {
			return err
		}
		return q.UpsertReportTotals(ctx, sqlc.UpsertReportTotalsParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(), Totals: raw, GeneratedBy: generatedBy,
		})
	})
	return report, err
}

func buildReport(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*MonthlyReport, error) {
	property, landlord, err := propertyAndLandlord(ctx, q, tenantID, propertyID)
	if err != nil {
		return nil, err
	}

	units, err := q.ListScheduleUnits(ctx, sqlc.ListScheduleUnitsParams{
		Period: period.FirstDay(), TenantID: tenantID, PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	payments, err := q.ListPeriodPayments(ctx, sqlc.ListPeriodPaymentsParams{
		TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	if err != nil {
		return nil, err
	}
	water, err := q.GetWaterSummary(ctx, sqlc.GetWaterSummaryParams{
		Rate: property.WaterRatePerUnit, TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
	})
	if err != nil {
		return nil, err
	}

	notes := []string{}
	saved, err := q.GetReport(ctx, sqlc.GetReportParams{TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay()})
	switch {
	case err == nil:
		if len(saved.Notes) > 0 {
			if jerr := json.Unmarshal(saved.Notes, &notes); jerr != nil {
				notes = []string{}
			}
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}

	// The GARBAGE column shows only if at least one unit actually billed
	// garbage this period — not just because the property has the
	// feature switched on (property.GarbageEnabled gates the feature;
	// each lease's GarbageBilled decides who's actually charged).
	anyGarbageBilled := false
	for _, u := range units {
		if u.GarbageBilled {
			anyGarbageBilled = true
			break
		}
	}

	r := &MonthlyReport{
		Period: period, PropertyName: property.Name, Location: property.Location,
		GarbageEnabled: property.GarbageEnabled && anyGarbageBilled, Notes: notes,
		ManagementFeePercent: property.ManagementFeePercent,
		Rows:                 make([]pdf.ScheduleRow, 0, len(units)),
		theme:                ResolveTheme(property, landlord),
	}

	index := make(map[uuid.UUID]int, len(units))
	for i, u := range units {
		row := pdf.ScheduleRow{HouseNo: u.UnitCode}
		if u.TenantName != "" {
			row.Tenants = append(row.Tenants, u.TenantName)
		}
		if u.PayerNames != "" {
			row.Tenants = append(row.Tenants, strings.Split(u.PayerNames, "\n")...)
		}
		r.Rows = append(r.Rows, row)
		index[u.UnitID] = i
		if u.Status == "occupied" {
			r.Summary.Occupied++
		} else {
			r.Summary.Vacant++
		}
	}

	for _, p := range payments {
		i, ok := index[p.UnitID]
		if !ok {
			continue
		}
		sp := pdf.SchedulePayment{Date: p.PaidOn, Amount: p.Amount}
		row := &r.Rows[i]
		switch p.LedgerType {
		case LedgerTypeRent:
			row.Rent = append(row.Rent, sp)
			r.Totals.Rent = r.Totals.Rent.Add(p.Amount)
		case LedgerTypeWater:
			row.Water = append(row.Water, sp)
			r.Totals.Water = r.Totals.Water.Add(p.Amount)
		case LedgerTypeGarbage:
			row.Garbage = append(row.Garbage, sp)
			if r.GarbageEnabled {
				r.Totals.Garbage = r.Totals.Garbage.Add(p.Amount)
			}
		case LedgerTypeRentDeposit:
			row.RentDeposit = append(row.RentDeposit, sp)
			r.Totals.RentDeposit = r.Totals.RentDeposit.Add(p.Amount)
		case LedgerTypeWaterDeposit:
			row.WaterDeposit = append(row.WaterDeposit, sp)
			r.Totals.WaterDeposit = r.Totals.WaterDeposit.Add(p.Amount)
		case LedgerTypeElectricityDeposit:
			row.ElectricityDeposit = append(row.ElectricityDeposit, sp)
			r.Totals.ElectricityDeposit = r.Totals.ElectricityDeposit.Add(p.Amount)
		}
	}

	// Never vanishes from a period's own history just because the property
	// later switches electricity off (see the ElectricityDepositShown doc
	// comment): "shown" is enabled-now OR paid-this-period, not just enabled.
	r.ElectricityDepositShown = property.ElectricityEnabled || r.Totals.ElectricityDeposit.IsPositive()

	r.Totals.Grand = r.Totals.Rent.Add(r.Totals.Water).Add(r.Totals.Garbage).Add(r.Totals.RentDeposit).Add(r.Totals.WaterDeposit)
	if r.ElectricityDepositShown {
		r.Totals.Grand = r.Totals.Grand.Add(r.Totals.ElectricityDeposit)
	}

	r.Summary.WaterUnits = water.UnitsConsumed
	r.Summary.WaterRate = property.WaterRatePerUnit
	r.Summary.ExpectedWater = water.Expected
	// ActualWater is what tenants paid (NOTE:1's "TOTAL WATER BILLS
	// PAYMENT"), not what was charged — see water.Billed, which the
	// confirmation sanity check compares against Expected instead.
	r.Summary.ActualWater = r.Totals.Water
	r.Deviation = r.Summary.Deviation()

	// The management fee is a percentage of rent collected, not the grand
	// total: water/garbage/deposit collections pass through untouched
	// (system-design.txt PDF confirmation addendum; matches the Rundas
	// source's "5/100 X KSH. 567,000" — rent only, water excluded).
	r.ManagementFee = r.Totals.Rent.PercentOf(int64(math.Round(property.ManagementFeePercent * 100)))
	return r, nil
}

// ReceiptModel is the service layer for receipts.
type ReceiptModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Generate lists the receipts already issued for the period (just unitID,
// if set) and renders them: receipts are created at posting time now (see
// issueReceipt in allocation.go), one per payment allocation, so this is a
// read, not a write. Returns ErrNothingToGenerate if none were issued.
func (m ReceiptModel) Generate(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period, unitID *uuid.UUID) ([]pdf.Receipt, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var receipts []pdf.Receipt
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		receipts = nil

		property, landlord, err := propertyAndLandlord(ctx, q, tenantID, propertyID)
		if err != nil {
			return err
		}
		rows, err := q.ListReceiptsForPeriod(ctx, sqlc.ListReceiptsForPeriodParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(), UnitID: unitID,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return ErrNothingToGenerate
		}

		theme := ResolveTheme(property, landlord)
		for _, row := range rows {
			method := pdf.MethodCash
			if row.Source != "manual" {
				method = pdf.MethodMpesa
			}
			ledgerType := LedgerTypeRent
			if row.LedgerType != nil {
				ledgerType = *row.LedgerType
			}
			var arrears string
			if row.ArrearsNote != nil {
				arrears = *row.ArrearsNote
			}
			var voidReason string
			if row.VoidReason != nil {
				voidReason = *row.VoidReason
			}

			r := pdf.Receipt{
				Theme: theme, PropertyName: property.Name, ReceiptNo: row.ReceiptNo,
				Date: row.ReceivedAt, Period: row.Period, SettledPeriod: row.SettledPeriod, HouseNo: row.UnitCode,
				LedgerType: ledgerType, Amount: row.Amount, ArrearsNote: arrears,
				Method: method, MpesaCode: row.MpesaReceipt,
				Void: row.VoidedAt != nil, VoidReason: voidReason,
			}
			if row.TenantName != "" {
				r.ReceivedFrom = append(r.ReceivedFrom, row.TenantName)
			}
			if row.PayerNames != "" {
				r.ReceivedFrom = append(r.ReceivedFrom, strings.Split(row.PayerNames, "\n")...)
			}
			receipts = append(receipts, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return receipts, nil
}
