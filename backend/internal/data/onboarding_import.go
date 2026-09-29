package data

import (
	"context"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Onboarding import: bring a client's existing building into Willcoll in one
// file. Each row is a unit, and (if it has a tenant) that tenant's lease with
// the position they are in at go-live:
//
//   - the deposits they have ALREADY paid (held), which are recorded as a lease
//     deposit plus an opening credit that settles it, so nothing shows as owing
//     and nothing shows as "paid this month" in a report;
//   - the arrears they owe today on rent, water and garbage, recorded as clearly
//     labelled OPENING_BALANCE debits.
//
// Everything in a file is written in ONE transaction: all rows or none. Nothing
// is ever edited afterwards: a wrong import is corrected with the ordinary
// reversal on the unit's ledger.

// MaxOnboardRows caps one import file.
const MaxOnboardRows = MaxImportUnits

// onboardTimeout allows a 500-row import more than the everyday query timeout.
const onboardTimeout = 30 * time.Second

// OnboardRow is one parsed row of the file, with its file row number.
type OnboardRow struct {
	Row         int
	UnitCode    string
	MeterNumber *string

	// The tenant. A row with no TenantName is a vacant unit and carries no
	// tenant or money columns.
	TenantName   string
	Phone        string
	Rent         moneyfmt.Money
	StartDate    *time.Time
	CoPayerName  *string
	CoPayerPhone *string

	// Deposits already held (paid before go-live).
	RentDeposit  moneyfmt.Money
	WaterDeposit moneyfmt.Money

	// Amounts owed today.
	RentArrears    moneyfmt.Money
	WaterArrears   moneyfmt.Money
	GarbageArrears moneyfmt.Money
}

// HasTenant reports whether the row creates a lease.
func (r OnboardRow) HasTenant() bool { return r.TenantName != "" }

// OnboardSummary is what an import did, or with a dry run would do.
type OnboardSummary struct {
	UnitsCreated      int            `json:"units_created"`
	LeasesCreated     int            `json:"leases_created"`
	VacantUnits       int            `json:"vacant_units"`
	MonthlyRent       moneyfmt.Money `json:"monthly_rent"`
	RentDepositsHeld  moneyfmt.Money `json:"rent_deposits_held"`
	WaterDepositsHeld moneyfmt.Money `json:"water_deposits_held"`
	RentArrears       moneyfmt.Money `json:"rent_arrears"`
	WaterArrears      moneyfmt.Money `json:"water_arrears"`
	GarbageArrears    moneyfmt.Money `json:"garbage_arrears"`
}

// ImportOnboarding writes the whole file, or with dryRun only checks it. asAt
// is the day the opening position is stated for (its month is the go-live
// month). It returns ErrPropertyNotFound, or an *ImportError listing every row
// that cannot be imported.
func (m LeaseModel) ImportOnboarding(ctx context.Context, tenantID, createdBy, propertyID uuid.UUID, asAt time.Time, rows []OnboardRow, dryRun bool) (*OnboardSummary, error) {
	ctx, cancel := withTimeout(ctx, onboardTimeout)
	defer cancel()

	var summary *OnboardSummary
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		summary = &OnboardSummary{} // rebuilt if the transaction is retried

		if err := checkPropertyActive(ctx, q, tenantID, propertyID); err != nil {
			return err
		}
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
		if err != nil {
			return notFound(err)
		}
		states, err := q.ListUnitStatesForProperty(ctx, sqlc.ListUnitStatesForPropertyParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		type state struct {
			id     uuid.UUID
			leased bool
		}
		existing := make(map[string]state, len(states))
		for _, s := range states {
			existing[s.UnitCode] = state{id: s.ID, leased: s.HasActiveLease}
		}

		// Check every row against what is already there before writing any.
		var problems []ImportRowError
		for _, r := range rows {
			if s, ok := existing[r.UnitCode]; ok && s.leased && r.HasTenant() {
				problems = append(problems, ImportRowError{Row: r.Row, Field: "unit_code", Message: "already has an active tenant; end that lease first, or remove this row"})
			}
			if r.GarbageArrears.IsPositive() && !property.GarbageEnabled {
				problems = append(problems, ImportRowError{Row: r.Row, Field: "garbage_arrears", Message: "garbage collection is off for this property, so it has no garbage ledger"})
			}
		}
		if len(problems) > 0 {
			return &ImportError{Rows: problems}
		}

		for _, r := range rows {
			s, isExisting := existing[r.UnitCode]
			if !isExisting {
				if !dryRun { // a dry run counts what it would do and writes nothing
					u, err := q.CreateUnit(ctx, sqlc.CreateUnitParams{TenantID: tenantID, PropertyID: propertyID, UnitCode: r.UnitCode, MeterNumber: r.MeterNumber, Status: UnitStatusVacant})
					if err != nil {
						return fmt.Errorf("creating unit %s: %w", r.UnitCode, err)
					}
					s = state{id: u.ID}
				}
				summary.UnitsCreated++
			}

			if !r.HasTenant() {
				summary.VacantUnits++
				continue
			}
			summary.LeasesCreated++
			summary.MonthlyRent = summary.MonthlyRent.Add(r.Rent)
			summary.RentDepositsHeld = summary.RentDepositsHeld.Add(r.RentDeposit)
			summary.WaterDepositsHeld = summary.WaterDepositsHeld.Add(r.WaterDeposit)
			summary.RentArrears = summary.RentArrears.Add(r.RentArrears)
			summary.WaterArrears = summary.WaterArrears.Add(r.WaterArrears)
			summary.GarbageArrears = summary.GarbageArrears.Add(r.GarbageArrears)
			if dryRun {
				continue
			}

			start := asAt
			if r.StartDate != nil {
				start = *r.StartDate
			}
			lease := &Lease{
				UnitID: s.id, TenantName: r.TenantName, PrimaryPhone: r.Phone, RentAmount: r.Rent,
				RentDepositAmount: r.RentDeposit, WaterDepositAmount: r.WaterDeposit, StartDate: start, Status: LeaseStatusActive,
			}
			if r.CoPayerName != nil {
				lease.Payers = []LeasePayer{{Name: *r.CoPayerName, Phone: r.CoPayerPhone}}
			}
			if err := createLeaseTx(ctx, q, tenantID, createdBy, lease); err != nil {
				return leaseImportError(r, err)
			}
			if err := postOpeningBalances(ctx, q, tenantID, createdBy, lease, asAt, r); err != nil {
				return fmt.Errorf("opening balances for %s: %w", r.UnitCode, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

// leaseImportError turns a lease-creation failure into a row error.
func leaseImportError(r OnboardRow, err error) error {
	if err == ErrDuplicateActiveLease {
		return &ImportError{Rows: []ImportRowError{{Row: r.Row, Field: "unit_code", Message: "already has an active tenant"}}}
	}
	return fmt.Errorf("creating the lease for %s: %w", r.UnitCode, err)
}

// postOpeningBalances records a new lease's starting position under ONE header:
//
//	arrears   DEBIT  via an opening_balance charge on the rent / water / garbage ledger
//	deposits  CREDIT that settles the lease-start deposit already posted for the
//	          lease (reference_type opening_balance: not a payment)
func postOpeningBalances(ctx context.Context, q sqlc.Querier, tenantID, createdBy uuid.UUID, lease *Lease, asAt time.Time, r OnboardRow) error {
	type debit struct {
		ledger string
		amount moneyfmt.Money
	}
	type credit struct {
		ledger string
		amount moneyfmt.Money
	}
	var debits []debit
	var credits []credit
	for _, d := range []debit{{LedgerTypeRent, r.RentArrears}, {LedgerTypeWater, r.WaterArrears}, {LedgerTypeGarbage, r.GarbageArrears}} {
		if d.amount.IsPositive() {
			debits = append(debits, d)
		}
	}
	for _, c := range []credit{{LedgerTypeRentDeposit, r.RentDeposit}, {LedgerTypeWaterDeposit, r.WaterDeposit}} {
		if c.amount.IsPositive() {
			credits = append(credits, c)
		}
	}
	if len(debits) == 0 && len(credits) == 0 {
		return nil
	}

	label := "as at " + asAt.Format("02 Jan 2006")
	headerID, err := q.CreateTransactionHeader(ctx, sqlc.CreateTransactionHeaderParams{
		TenantID:       tenantID,
		Type:           TxTypeOpeningBalance,
		IdempotencyKey: "opening:" + lease.ID.String(), // a lease's opening position is posted once
		Description:    "Opening balance " + label,
		CreatedBy:      createdBy.String(),
	})
	if err != nil {
		return err
	}

	for _, d := range debits {
		accountID, err := q.GetOrCreateLedgerAccount(ctx, sqlc.GetOrCreateLedgerAccountParams{TenantID: tenantID, UnitID: lease.UnitID, Type: d.ledger})
		if err != nil {
			return err
		}
		chargeID, err := q.CreateCharge(ctx, sqlc.CreateChargeParams{
			TenantID: tenantID, LedgerAccountID: accountID, SourceType: ChargeSourceOpening,
			Period: moneyfmt.NewPeriod(asAt).FirstDay(), Amount: d.amount,
			Description: "Balance brought forward " + label, CreatedBy: createdBy,
		})
		if err != nil {
			return err
		}
		if _, err := q.CreateLedgerEntry(ctx, sqlc.CreateLedgerEntryParams{
			TenantID: tenantID, TransactionHeaderID: headerID, LedgerAccountID: accountID,
			Direction: DirectionDebit, Amount: d.amount, ReferenceType: ReferenceTypeCharge, ReferenceID: chargeID,
		}); err != nil {
			return err
		}
	}
	for _, c := range credits {
		accountID, err := q.GetOrCreateLedgerAccount(ctx, sqlc.GetOrCreateLedgerAccountParams{TenantID: tenantID, UnitID: lease.UnitID, Type: c.ledger})
		if err != nil {
			return err
		}
		if _, err := q.CreateLedgerEntry(ctx, sqlc.CreateLedgerEntryParams{
			TenantID: tenantID, TransactionHeaderID: headerID, LedgerAccountID: accountID,
			Direction: DirectionCredit, Amount: c.amount, ReferenceType: ReferenceTypeOpeningBalance, ReferenceID: lease.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}
