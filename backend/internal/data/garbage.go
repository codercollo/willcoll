package data

import (
	"context"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

const garbageRunConstraint = "garbage_runs_property_id_period_key"

// GarbageRow is one occupied unit on the garbage preview. Every occupied
// unit gets a row, whether or not its lease has garbage billing turned on
// (Billed false): the UI shows "Not billed" instead of hiding the unit.
type GarbageRow struct {
	UnitID       uuid.UUID      `json:"unit_id"`
	UnitCode     string         `json:"unit_code"`
	TenantName   string         `json:"tenant_name"`
	Billed       bool           `json:"billed"`
	Fee          moneyfmt.Money `json:"fee"`
	PriorBalance moneyfmt.Money `json:"prior_balance"`
	TotalDue     moneyfmt.Money `json:"total_due"`
}

// GarbagePreview is what a run would bill for one period.
type GarbagePreview struct {
	Period           moneyfmt.Period `json:"period"`
	Enabled          bool            `json:"enabled"`
	Fee              moneyfmt.Money  `json:"fee"`
	AlreadyGenerated bool            `json:"already_generated"`
	BilledCount      int             `json:"billed_count"`
	Message          string          `json:"message"`
	Rows             []GarbageRow    `json:"rows"`
}

// garbagePreviewMessage is the accurate, actionable summary line shown above
// the garbage tab's unit list and generate dialog.
func garbagePreviewMessage(billedCount int, fee moneyfmt.Money) string {
	if billedCount == 0 {
		return "No units are set to be billed for garbage. Turn it on in the lease."
	}
	return fmt.Sprintf("%d units have garbage billing on (Ksh %s each)", billedCount, fee.String())
}

// GarbageModel is the service layer for garbage runs.
type GarbageModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Preview returns the units a run would bill and their current balances. It
// works whether or not garbage billing is enabled, so the UI can show why a
// run isn't available.
func (m GarbageModel) Preview(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*GarbagePreview, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var preview GarbagePreview
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
		if err != nil {
			return notFound(err)
		}
		units, err := q.ListGarbageUnitsForPeriod(ctx, sqlc.ListGarbageUnitsForPeriodParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		exists, err := q.GarbageRunExists(ctx, sqlc.GarbageRunExistsParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		if err != nil {
			return err
		}

		billedCount := 0
		preview = GarbagePreview{
			Period: period, Enabled: property.GarbageEnabled, Fee: property.GarbageFee,
			AlreadyGenerated: exists, Rows: make([]GarbageRow, 0, len(units)),
		}
		for _, u := range units {
			row := GarbageRow{
				UnitID: u.UnitID, UnitCode: u.UnitCode, TenantName: u.TenantName,
				Billed: u.Billed, PriorBalance: u.PriorBalance, TotalDue: u.PriorBalance,
			}
			if u.Billed {
				billedCount++
				row.Fee = property.GarbageFee
				row.TotalDue = u.PriorBalance.Add(property.GarbageFee)
			}
			preview.Rows = append(preview.Rows, row)
		}
		preview.BilledCount = billedCount
		preview.Message = garbagePreviewMessage(billedCount, property.GarbageFee)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &preview, nil
}

// Generate bills the property's fixed garbage fee to every occupied unit in
// one serializable transaction: the run row, then a charge and DEBIT entry
// per unit. Fails with ErrGarbageNotEnabled if billing is off,
// ErrNothingToGenerate if the fee is zero or no unit is occupied,
// ErrNoUnitsBilledForGarbage if units are occupied but none has garbage
// billing turned on in its lease, and ErrRunAlreadyGenerated on a second
// run for the period.
func (m GarbageModel) Generate(ctx context.Context, tenantID, createdBy, propertyID uuid.UUID, period moneyfmt.Period) (*RunResult, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var result RunResult
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		result = RunResult{}

		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
		if err != nil {
			return notFound(err)
		}
		if !property.GarbageEnabled {
			return ErrGarbageNotEnabled
		}
		if !property.GarbageFee.IsPositive() {
			return ErrNothingToGenerate
		}
		allUnits, err := q.ListGarbageUnitsForPeriod(ctx, sqlc.ListGarbageUnitsForPeriodParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		if len(allUnits) == 0 {
			return ErrNothingToGenerate
		}
		units := make([]sqlc.ListGarbageUnitsForPeriodRow, 0, len(allUnits))
		for _, u := range allUnits {
			if u.Billed {
				units = append(units, u)
			}
		}
		if len(units) == 0 {
			return ErrNoUnitsBilledForGarbage
		}

		runID, err := q.CreateGarbageRun(ctx, sqlc.CreateGarbageRunParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(), FeeSnapshot: property.GarbageFee,
		})
		if err != nil {
			if isUniqueViolation(err, garbageRunConstraint) {
				return ErrRunAlreadyGenerated
			}
			return err
		}

		charges := make([]debitCharge, 0, len(units))
		for _, u := range units {
			id := runID
			charges = append(charges, debitCharge{
				UnitID: u.UnitID, LedgerType: LedgerTypeGarbage,
				Charge: Charge{
					SourceType: ChargeSourceGarbageRun, SourceID: &id,
					Period: period.FirstDay(), Amount: property.GarbageFee,
					Description: "Garbage " + period.String(), CreatedBy: createdBy,
				},
			})
			result.Total = result.Total.Add(property.GarbageFee)
		}

		err = postCharges(ctx, q, tenantID, TransactionHeader{
			Type:           TxTypeGarbageRun,
			IdempotencyKey: fmt.Sprintf("garbage-run:%s:%s", propertyID, period),
			Description:    "Garbage run " + period.String(),
			CreatedBy:      createdBy.String(),
		}, charges)
		if err != nil {
			if isUniqueViolation(err, headerKeyConstraint) {
				return ErrRunAlreadyGenerated
			}
			return err
		}
		result.Billed = len(units)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Invoices returns the garbage bills of a period's run (one unit if unitID
// is set), ready for pdf.BuildGarbageInvoices. Nothing is stored: the
// previous balance B/F is recomputed from the ledger as it stood before the
// run. Returns ErrRecordNotFound if the period was never billed.
func (m GarbageModel) Invoices(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period, unitID *uuid.UUID) ([]pdf.GarbageInvoice, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var invoices []pdf.GarbageInvoice
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		property, landlord, err := propertyAndLandlord(ctx, q, tenantID, propertyID)
		if err != nil {
			return err
		}
		if !property.GarbageEnabled {
			return ErrGarbageNotEnabled
		}
		rows, err := q.ListGarbageInvoices(ctx, sqlc.ListGarbageInvoicesParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(), UnitID: unitID,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return ErrRecordNotFound
		}

		theme := ResolveTheme(property, landlord)
		payment := ResolvePaymentParticulars(property, landlord)
		if err := requirePaymentParticulars(payment); err != nil {
			return err
		}
		theme.ReconnectionNote = reconnectionNoteFor(property, theme)
		invoices = make([]pdf.GarbageInvoice, 0, len(rows))
		for _, r := range rows {
			invoices = append(invoices, pdf.GarbageInvoice{
				Theme: theme, CustomerName: r.TenantName, HouseNo: r.UnitCode,
				Period: r.Period, BillDate: r.BillDate, Fee: r.Fee,
				PriorBalance: r.PriorBalance, Payment: payment,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return invoices, nil
}
