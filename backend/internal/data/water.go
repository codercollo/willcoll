package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrReadingLocked is returned when a saved reading was already billed.
	ErrReadingLocked = errors.New("water reading is locked")

	// ErrUnitNotInProperty is returned when a reading names a unit that
	// isn't an occupied unit of the property.
	ErrUnitNotInProperty = errors.New("unit is not an occupied unit of this property")

	// ErrInvalidReading is returned when a current reading is below the
	// previous one, or a rate is negative.
	ErrInvalidReading = errors.New("invalid meter reading")

	// ErrRunAlreadyGenerated is returned when a billing run for the same
	// property and period has already been posted.
	ErrRunAlreadyGenerated = errors.New("billing run already generated for this period")

	// ErrNothingToGenerate is returned when a run has no draft to bill.
	ErrNothingToGenerate = errors.New("nothing to generate for this period")

	// ErrGarbageNotEnabled is returned when garbage billing is switched off
	// for the property.
	ErrGarbageNotEnabled = errors.New("garbage billing is not enabled for this property")

	// ErrNoUnitsBilledForGarbage is returned when a garbage run has no
	// billable units: the property has occupied units, but none of their
	// leases have garbage billing turned on. Distinct from
	// ErrNothingToGenerate so the client can show an actionable message
	// instead of a generic one.
	ErrNoUnitsBilledForGarbage = errors.New("no units are set to be billed for garbage; turn it on in the lease")

	// ErrPaymentParticularsMissing is returned when a water or garbage
	// bill would print blank "pay to" details: a property (or its
	// landlord) with no bank/paybill details configured at all.
	ErrPaymentParticularsMissing = errors.New("payment particulars are not configured for this property")
)

// requirePaymentParticulars blocks bill generation rather than printing a
// blank paybill/account number a tenant could mistake for "pay nothing."
// AccountName always defaults to the property's own name (ResolveTheme),
// so it's never the missing piece — the account number is: without it,
// there is nowhere to actually send the money.
func requirePaymentParticulars(p pdf.PaymentParticulars) error {
	if p.AccountNumber == "" {
		return ErrPaymentParticularsMissing
	}
	return nil
}

// WaterGridRow is one unit's line of the draft water grid (system-design.txt
// 3.9). For a unit with no saved reading the reading fields other than
// PreviousReading are zero and HasReading is false.
type WaterGridRow struct {
	UnitID          uuid.UUID      `json:"unit_id"`
	UnitCode        string         `json:"unit_code"`
	TenantName      string         `json:"tenant_name"`
	HasReading      bool           `json:"has_reading"`
	PreviousReading moneyfmt.Money `json:"previous_reading"`
	CurrentReading  moneyfmt.Money `json:"current_reading"`
	UnitsConsumed   moneyfmt.Money `json:"units_consumed"`
	Rate            moneyfmt.Money `json:"rate"`
	Amount          moneyfmt.Money `json:"amount"`
	PriorBalance    moneyfmt.Money `json:"prior_balance"`
	TotalDue        moneyfmt.Money `json:"total_due"`
	Locked          bool           `json:"locked"`
}

// WaterGrid is the property's draft grid for one period.
type WaterGrid struct {
	Period      moneyfmt.Period `json:"period"`
	DefaultRate moneyfmt.Money  `json:"default_rate"`
	Rows        []WaterGridRow  `json:"rows"`
}

// WaterReadingInput is one row of a bulk save. PreviousReading and Rate
// default to last period's closing reading and the property's rate.
type WaterReadingInput struct {
	UnitID          uuid.UUID
	CurrentReading  moneyfmt.Money
	PreviousReading *moneyfmt.Money
	Rate            *moneyfmt.Money
	// ReadingDate is when the meter was actually read — printed on the
	// water bill. Zero means today: a manager entering today's reading
	// doesn't have to type today's date.
	ReadingDate time.Time
}

// RunResult summarises a posted billing run.
type RunResult struct {
	Billed int            `json:"billed"`
	Total  moneyfmt.Money `json:"total"`
	// Missing lists occupied units that had no reading (water) so the
	// manager can see who was left out; empty for garbage.
	Missing []string `json:"missing,omitempty"`
}

// WaterModel is the service layer for water readings and runs.
type WaterModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

func waterRowFromGrid(r sqlc.ListWaterGridRow) WaterGridRow {
	return WaterGridRow{
		UnitID: r.UnitID, UnitCode: r.UnitCode, TenantName: r.TenantName,
		HasReading: r.HasReading, PreviousReading: r.PreviousReading,
		CurrentReading: r.CurrentReading, UnitsConsumed: r.UnitsConsumed,
		Rate: r.Rate, Amount: r.Amount, PriorBalance: r.PriorBalance,
		TotalDue: r.PriorBalance.Add(r.Amount), Locked: r.Locked,
	}
}

// Grid returns the draft grid: one row per occupied unit. It returns
// ErrRecordNotFound if the property isn't the tenant's.
func (m WaterModel) Grid(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) (*WaterGrid, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var grid WaterGrid
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
		if err != nil {
			return notFound(err)
		}
		rows, err := q.ListWaterGrid(ctx, sqlc.ListWaterGridParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		if err != nil {
			return err
		}
		grid = WaterGrid{Period: period, DefaultRate: property.WaterRatePerUnit, Rows: make([]WaterGridRow, 0, len(rows))}
		for _, r := range rows {
			grid.Rows = append(grid.Rows, waterRowFromGrid(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &grid, nil
}

// SaveReadings saves draft readings for the period in one transaction:
// either every row is saved or none. Errors wrap ErrUnitNotInProperty,
// ErrInvalidReading or ErrReadingLocked and name the unit.
func (m WaterModel) SaveReadings(ctx context.Context, tenantID, recordedBy, propertyID uuid.UUID, period moneyfmt.Period, inputs []WaterReadingInput) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
		if err != nil {
			return notFound(err)
		}
		rows, err := q.ListWaterGrid(ctx, sqlc.ListWaterGridParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		if err != nil {
			return err
		}
		byUnit := make(map[uuid.UUID]sqlc.ListWaterGridRow, len(rows))
		for _, r := range rows {
			byUnit[r.UnitID] = r
		}

		for _, in := range inputs {
			row, ok := byUnit[in.UnitID]
			if !ok {
				return fmt.Errorf("%w: %s", ErrUnitNotInProperty, in.UnitID)
			}
			if row.Locked {
				return fmt.Errorf("%w: %s", ErrReadingLocked, row.UnitCode)
			}

			previous := row.PreviousReading
			if in.PreviousReading != nil {
				previous = *in.PreviousReading
			}
			rate := property.WaterRatePerUnit
			if in.Rate != nil {
				rate = *in.Rate
			}
			if in.CurrentReading.IsNegative() || previous.IsNegative() || rate.IsNegative() {
				return fmt.Errorf("%w: %s: readings and rate must not be negative", ErrInvalidReading, row.UnitCode)
			}
			if in.CurrentReading.Cmp(previous) < 0 {
				return fmt.Errorf("%w: %s: current reading is below the previous reading", ErrInvalidReading, row.UnitCode)
			}

			readingDate := in.ReadingDate
			if readingDate.IsZero() {
				readingDate = time.Now()
			}
			_, err := q.UpsertWaterReading(ctx, sqlc.UpsertWaterReadingParams{
				TenantID: tenantID, UnitID: in.UnitID, Period: period.FirstDay(),
				PreviousReading: previous, CurrentReading: in.CurrentReading,
				RateSnapshot: rate, RecordedBy: recordedBy, ReadingDate: readingDate,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: %s", ErrReadingLocked, row.UnitCode)
				}
				return err
			}
		}
		return nil
	})
}

// isUniqueViolation reports whether err is a Postgres unique violation of
// the named constraint.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == constraint
}

const headerKeyConstraint = "transaction_headers_idempotency_key_key"

// Generate is "Generate billing invoices": in one serializable transaction
// it posts a charge and a DEBIT entry for every unlocked reading and locks
// them all (system-design.txt 3.9 step 3). A second run for the same
// property and period fails with ErrRunAlreadyGenerated: the header's
// idempotency key is unique, so a double click or retry cannot bill twice.
// Zero-consumption readings are locked but post nothing (ledger amounts
// must be positive).
func (m WaterModel) Generate(ctx context.Context, tenantID, createdBy, propertyID uuid.UUID, period moneyfmt.Period) (*RunResult, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var result RunResult
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		result = RunResult{Total: moneyfmt.Money{}}

		if _, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID}); err != nil {
			return notFound(err)
		}
		readings, err := q.ListWaterReadingsForRun(ctx, sqlc.ListWaterReadingsForRunParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		if err != nil {
			return err
		}

		var drafts []sqlc.ListWaterReadingsForRunRow
		for _, r := range readings {
			if !r.Locked {
				drafts = append(drafts, r)
			}
		}
		if len(drafts) == 0 {
			return ErrNothingToGenerate
		}

		var charges []debitCharge
		for _, r := range drafts {
			if !r.Amount.IsPositive() {
				continue
			}
			id := r.ID
			charges = append(charges, debitCharge{
				UnitID: r.UnitID, LedgerType: LedgerTypeWater,
				Charge: Charge{
					SourceType: ChargeSourceWaterReading, SourceID: &id,
					Period: period.FirstDay(), Amount: r.Amount,
					Description: "Water " + period.String(), CreatedBy: createdBy,
				},
			})
			result.Total = result.Total.Add(r.Amount)
		}

		if len(charges) > 0 {
			err := postCharges(ctx, q, tenantID, TransactionHeader{
				Type:           TxTypeWaterRun,
				IdempotencyKey: fmt.Sprintf("water-run:%s:%s", propertyID, period),
				Description:    "Water run " + period.String(),
				CreatedBy:      createdBy.String(),
			}, charges)
			if err != nil {
				if isUniqueViolation(err, headerKeyConstraint) {
					return ErrRunAlreadyGenerated
				}
				return err
			}
		}

		for _, r := range drafts {
			n, err := q.LockWaterReading(ctx, sqlc.LockWaterReadingParams{TenantID: tenantID, ID: r.ID})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrReadingLocked
			}
		}
		result.Billed = len(drafts)

		grid, err := q.ListWaterGrid(ctx, sqlc.ListWaterGridParams{
			TenantID: tenantID, PropertyID: propertyID, Period: period.FirstDay(),
		})
		if err != nil {
			return err
		}
		result.Missing = nil
		for _, g := range grid {
			if !g.HasReading {
				result.Missing = append(result.Missing, g.UnitCode)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Invoices returns the water bills of a period's locked readings (one unit
// if unitID is set), ready for pdf.BuildWaterInvoices. Nothing is stored:
// "previous balance B/F" is recomputed from the ledger as it stood before
// the run. Returns ErrRecordNotFound if there is nothing billed.
func (m WaterModel) Invoices(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period, unitID *uuid.UUID) ([]pdf.WaterInvoice, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var invoices []pdf.WaterInvoice
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		property, landlord, err := propertyAndLandlord(ctx, q, tenantID, propertyID)
		if err != nil {
			return err
		}
		rows, err := q.ListWaterInvoices(ctx, sqlc.ListWaterInvoicesParams{
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
		invoices = make([]pdf.WaterInvoice, 0, len(rows))
		for _, r := range rows {
			invoices = append(invoices, pdf.WaterInvoice{
				Theme: theme, CustomerName: r.TenantName, HouseNo: r.UnitCode,
				Period: r.Period, ReadingDate: r.ReadingDate,
				CurrentReading: r.CurrentReading, PreviousReading: r.PreviousReading,
				UnitsConsumed: r.UnitsConsumed, Rate: r.Rate, Amount: r.Amount,
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

// propertyAndLandlord loads a property and its landlord for theming.
func propertyAndLandlord(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID) (*Property, *Landlord, error) {
	row, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
	if err != nil {
		return nil, nil, notFound(err)
	}
	property := propertyFromRow(row)

	lrow, err := q.GetLandlord(ctx, sqlc.GetLandlordParams{TenantID: tenantID, ID: property.LandlordID})
	if err != nil {
		return nil, nil, notFound(err)
	}
	landlord := landlordFromRow(lrow)
	return &property, &landlord, nil
}
