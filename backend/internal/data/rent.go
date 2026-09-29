package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

var (
	// ErrNoActiveLease is returned when a rent operation names a unit with
	// no active lease on the property.
	ErrNoActiveLease = errors.New("unit has no active lease")

	// ErrInvalidAmount is returned for a zero or negative amount.
	ErrInvalidAmount = errors.New("amount must be greater than zero")

	// ErrPeriodTooFar is returned when billing rent beyond next month.
	ErrPeriodTooFar = errors.New("cannot bill rent beyond next calendar month")

	// ErrAlreadyReversed is returned when an entry already has a reversal.
	ErrAlreadyReversed = errors.New("ledger entry is already reversed")

	// ErrNotReversible is returned when reversing a reversal entry: a
	// mistaken reversal is corrected with a fresh entry, not another storno.
	ErrNotReversible = errors.New("a reversal entry cannot itself be reversed")

	// ErrReasonRequired is returned when a reversal has no reason.
	ErrReasonRequired = errors.New("a reason is required")

	// ErrDuplicatePayment is returned when a manual payment reference was
	// already recorded, so a double submit cannot credit a tenant twice.
	ErrDuplicatePayment = errors.New("a payment with this reference was already recorded")

	// ErrInvalidInput is returned for a malformed rent request the service
	// layer rejects (unknown ledger type, oversized text, ...).
	ErrInvalidInput = errors.New("invalid input")
)

const (
	maxReasonLength    = 500
	maxReferenceLength = 100
)

// Status of a unit in the rent overview.
const (
	RentStatusPaid    = "paid"
	RentStatusPartial = "partial"
	RentStatusArrears = "arrears"
)

// manualLedgerTypes are the ledgers a manager can record a payment or charge
// against by hand.
var manualLedgerTypes = map[string]bool{
	LedgerTypeRent: true, LedgerTypeRentDeposit: true, LedgerTypeWater: true,
	LedgerTypeWaterDeposit: true, LedgerTypeGarbage: true, LedgerTypeElectricityDeposit: true,
}

// RentModel is the service layer for the rent domain: expected rent, monthly
// runs, manual payments, reversals and the collection overview. Rent uses
// the same ledger mechanics as water and garbage; only rent_runs is its own
// table.
type RentModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration

	// Now returns the current time; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (m RentModel) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// UnitRentInput is one unit's expected monthly rent.
type UnitRentInput struct {
	UnitID     uuid.UUID
	RentAmount moneyfmt.Money
}

// RunSummary is the result of a monthly rent run.
type RunSummary struct {
	Period  moneyfmt.Period `json:"period"`
	Billed  int             `json:"billed"`
	Skipped int             `json:"skipped"`
	Total   moneyfmt.Money  `json:"total_amount"`
}

// OverviewRow is one unit on the property Rent tab.
type OverviewRow struct {
	UnitID     uuid.UUID      `json:"unit_id"`
	UnitCode   string         `json:"unit_code"`
	TenantName string         `json:"tenant_name"`
	Expected   moneyfmt.Money `json:"expected"`
	Billed     bool           `json:"billed"`
	Paid       moneyfmt.Money `json:"paid_this_period"`
	Balance    moneyfmt.Money `json:"balance"`
	Status     string         `json:"status"`
}

// rentStatus derives the display status from the balance and what was paid
// this period: nothing owed is paid, something owed after a payment is
// partial, something owed and nothing paid is arrears.
func rentStatus(balance, paid moneyfmt.Money) string {
	switch {
	case !balance.IsPositive():
		return RentStatusPaid
	case paid.IsPositive():
		return RentStatusPartial
	default:
		return RentStatusArrears
	}
}

// SetExpectedRent sets one unit's expected monthly rent on its active lease.
// It posts nothing: only later rent runs pick up the new amount.
func (m RentModel) SetExpectedRent(ctx context.Context, tenantID, unitID uuid.UUID, amount moneyfmt.Money) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if !amount.IsPositive() {
		return ErrInvalidAmount
	}
	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}
		lease, err := q.GetActiveLeaseByUnit(ctx, sqlc.GetActiveLeaseByUnitParams{TenantID: tenantID, UnitID: unitID})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoActiveLease
			}
			return err
		}
		_, err = q.UpdateLeaseRentAmount(ctx, sqlc.UpdateLeaseRentAmountParams{
			TenantID: tenantID, ID: lease.ID, RentAmount: amount,
		})
		return notFoundToNoLease(err)
	})
}

func notFoundToNoLease(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoActiveLease
	}
	return err
}

// BulkSetExpectedRent saves the rent schedule spreadsheet in one
// transaction: if any row is invalid, none is saved. Errors name the unit.
func (m RentModel) BulkSetExpectedRent(ctx context.Context, tenantID, propertyID uuid.UUID, inputs []UnitRentInput) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID}); err != nil {
			return notFound(err)
		}
		leases, err := q.ListActiveLeasesForProperty(ctx, sqlc.ListActiveLeasesForPropertyParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		byUnit := make(map[uuid.UUID]sqlc.ListActiveLeasesForPropertyRow, len(leases))
		for _, l := range leases {
			byUnit[l.UnitID] = l
		}

		for _, in := range inputs {
			lease, ok := byUnit[in.UnitID]
			if !ok {
				return fmt.Errorf("%w: %s", ErrNoActiveLease, in.UnitID)
			}
			if !in.RentAmount.IsPositive() {
				return fmt.Errorf("%w: %s", ErrInvalidAmount, lease.UnitCode)
			}
			if _, err := q.UpdateLeaseRentAmount(ctx, sqlc.UpdateLeaseRentAmountParams{
				TenantID: tenantID, ID: lease.LeaseID, RentAmount: in.RentAmount,
			}); err != nil {
				return notFoundToNoLease(err)
			}
		}
		return nil
	})
}

// GenerateForPeriod bills the month's rent: for every active lease without a
// rent run for the period it records the run (snapshotting the lease rent
// now), then posts a RENT_RUN header, a charge and a DEBIT entry. All in one
// serializable transaction, retried on serialization failure. Units already
// billed, on a lease that starts after the period, or with no rent set are
// skipped, so re-running a period is safe.
func (m RentModel) GenerateForPeriod(ctx context.Context, tenantID, createdBy, propertyID uuid.UUID, period moneyfmt.Period) (*RunSummary, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if period.FirstDay().After(moneyfmt.NewPeriod(m.now()).Next().FirstDay()) {
		return nil, ErrPeriodTooFar
	}

	var summary RunSummary
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		summary = RunSummary{Period: period}

		if _, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID}); err != nil {
			return notFound(err)
		}
		leases, err := q.ListActiveLeasesForProperty(ctx, sqlc.ListActiveLeasesForPropertyParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}

		periodEnd := period.Next().FirstDay()
		for _, l := range leases {
			if !l.StartDate.Before(periodEnd) || !l.RentAmount.IsPositive() {
				summary.Skipped++
				continue
			}

			runID, err := q.InsertRentRun(ctx, sqlc.InsertRentRunParams{
				TenantID: tenantID, UnitID: l.UnitID, Period: period.FirstDay(), AmountSnapshot: l.RentAmount,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) { // already billed for the period
					summary.Skipped++
					continue
				}
				return err
			}

			err = postCharges(ctx, q, tenantID, TransactionHeader{
				Type:           TxTypeRentRun,
				IdempotencyKey: "rent-run:" + runID.String(),
				Description:    "Rent " + period.String(),
				CreatedBy:      createdBy.String(),
			}, []debitCharge{{
				UnitID: l.UnitID, LedgerType: LedgerTypeRent,
				Charge: Charge{
					SourceType: ChargeSourceRentRun, SourceID: &runID, Period: period.FirstDay(),
					Amount: l.RentAmount, Description: "Rent " + period.String(), CreatedBy: createdBy,
				},
			}})
			if err != nil {
				return err
			}
			summary.Billed++
			summary.Total = summary.Total.Add(l.RentAmount)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// PaymentInput is a payment a manager records by hand.
type PaymentInput struct {
	Amount     moneyfmt.Money
	Source     string // "manual" (cash) or "bank"
	Reference  string // bank or cash-book reference; unique among manual payments
	Note       string
	LedgerType string // defaults to RENT
}

// ManualPayment identifies what PostPayment recorded.
type ManualPayment struct {
	PaymentID uuid.UUID      `json:"payment_id"`
	Receipt   string         `json:"receipt"`
	Amount    moneyfmt.Money `json:"amount"`
	Ledger    string         `json:"ledger_type"`
}

// PostPayment records a payment received outside PayHero (cash or bank
// transfer) and credits the unit's ledger, in one serializable transaction
// through the same posting path as PayHero payments, including the zero-sum
// assertion. Paying more than is owed leaves the account in credit.
func (m RentModel) PostPayment(ctx context.Context, tenantID, managerID, unitID uuid.UUID, in PaymentInput) (*ManualPayment, error) {
	ctx, cancel := withTimeout(ctx, postingTimeout(m.Timeout))
	defer cancel()

	if in.LedgerType == "" {
		in.LedgerType = LedgerTypeRent
	}
	in.Reference = strings.TrimSpace(in.Reference)
	switch {
	case !in.Amount.IsPositive():
		return nil, ErrInvalidAmount
	case in.Source != "manual" && in.Source != "bank":
		return nil, fmt.Errorf("%w: source must be manual or bank", ErrInvalidInput)
	case !manualLedgerTypes[in.LedgerType]:
		return nil, fmt.Errorf("%w: unknown ledger type", ErrInvalidInput)
	case len(in.Reference) > maxReferenceLength || len(in.Note) > 500:
		return nil, fmt.Errorf("%w: reference or note too long", ErrInvalidInput)
	}

	var result ManualPayment
	err := m.Store.ExecTenantTxExclusive(ctx, tenantID, lockPayments, func(q sqlc.Querier) error {
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}

		if in.Reference != "" {
			_, err := q.GetManualPaymentByReference(ctx, sqlc.GetManualPaymentByReferenceParams{TenantID: tenantID, AccountReference: &in.Reference})
			if err == nil {
				return ErrDuplicatePayment
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}

		msisdn, payer := "manual", (*string)(nil)
		if lease, err := q.GetActiveLeaseByUnit(ctx, sqlc.GetActiveLeaseByUnitParams{TenantID: tenantID, UnitID: unitID}); err == nil {
			msisdn, payer = lease.PrimaryPhone, &lease.TenantName
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		raw, err := json.Marshal(map[string]string{
			"entered_by": managerID.String(), "kind": in.Source, "reference": in.Reference, "note": in.Note,
		})
		if err != nil {
			return err
		}
		var ref *string
		if in.Reference != "" {
			ref = &in.Reference
		}

		receipt := "MANUAL-" + strings.ToUpper(uuid.NewString()[:13])
		payment, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
			TenantID: tenantID, Source: "manual", MpesaReceipt: receipt, Amount: in.Amount,
			Msisdn: msisdn, PayerName: payer, AccountReference: ref, Status: "matched",
			RawPayload: raw, ReceivedAt: m.now(),
		})
		if err != nil {
			return err
		}

		if err := postPaymentAllocations(ctx, q, tenantID, payment, unitID,
			[]Allocation{{LedgerType: in.LedgerType, Amount: in.Amount}}, managerID.String(),
			PaymentOutcome{Status: PaymentStatusAllocated}); err != nil {
			return err
		}
		result = ManualPayment{PaymentID: payment.ID, Receipt: receipt, Amount: in.Amount, Ledger: in.LedgerType}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// PostManualCharge posts an ad-hoc charge (a penalty, or the corrected entry
// that follows a reversal) as a MANUAL_ADJUSTMENT: a charge and a DEBIT entry.
func (m RentModel) PostManualCharge(ctx context.Context, tenantID, managerID, unitID uuid.UUID, ledgerType string, amount moneyfmt.Money, description string) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if ledgerType == "" {
		ledgerType = LedgerTypeRent
	}
	description = strings.TrimSpace(description)
	switch {
	case !amount.IsPositive():
		return ErrInvalidAmount
	case !manualLedgerTypes[ledgerType]:
		return fmt.Errorf("%w: unknown ledger type", ErrInvalidInput)
	case description == "" || len(description) > maxReasonLength:
		return fmt.Errorf("%w: a description of up to 500 characters is required", ErrInvalidInput)
	}

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}
		return postCharges(ctx, q, tenantID, TransactionHeader{
			Type:           TxTypeManualAdjustment,
			IdempotencyKey: "manual-charge:" + uuid.NewString(),
			Description:    description,
			CreatedBy:      managerID.String(),
		}, []debitCharge{{
			UnitID: unitID, LedgerType: ledgerType,
			Charge: Charge{
				SourceType: ChargeSourceManual, Period: moneyfmt.NewPeriod(m.now()).FirstDay(),
				Amount: amount, Description: description, CreatedBy: managerID,
			},
		}})
	})
}

// ReverseEntry is the storno: it never touches the original entry. It
// appends a REVERSAL header carrying the reason and one mirrored entry (same
// account and amount, direction flipped) pointing back at the original, so
// the mistake and its correction both stay visible. Returns the new entry id.
// An entry can be reversed once; a reversal cannot be reversed. The header
// idempotency key "reversal:<entry id>" enforces the first rule in the
// database too, so two concurrent requests cannot both reverse.
func (m RentModel) ReverseEntry(ctx context.Context, tenantID, managerID, entryID uuid.UUID, reason string) (uuid.UUID, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	reason = strings.TrimSpace(reason)
	if reason == "" {
		return uuid.Nil, ErrReasonRequired
	}
	if len(reason) > maxReasonLength {
		return uuid.Nil, fmt.Errorf("%w: reason must be at most 500 characters", ErrInvalidInput)
	}

	var reversalID uuid.UUID
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		orig, err := q.GetLedgerEntryForReversal(ctx, sqlc.GetLedgerEntryForReversalParams{TenantID: tenantID, ID: entryID})
		if err != nil {
			return notFound(err)
		}
		switch {
		case orig.ReferenceType == ReferenceTypeReversal:
			return ErrNotReversible
		case orig.Reversed:
			return ErrAlreadyReversed
		}

		headerID, err := q.CreateTransactionHeader(ctx, sqlc.CreateTransactionHeaderParams{
			TenantID:       tenantID,
			Type:           TxTypeReversal,
			IdempotencyKey: "reversal:" + entryID.String(),
			Description:    fmt.Sprintf("Reversal of entry %s: %s", entryID, reason),
			CreatedBy:      managerID.String(),
		})
		if err != nil {
			if isUniqueViolation(err, headerKeyConstraint) {
				return ErrAlreadyReversed
			}
			return err
		}

		direction := DirectionCredit
		if orig.Direction == DirectionCredit {
			direction = DirectionDebit
		}
		reversalID, err = q.CreateLedgerEntry(ctx, sqlc.CreateLedgerEntryParams{
			TenantID:            tenantID,
			TransactionHeaderID: headerID,
			LedgerAccountID:     orig.LedgerAccountID,
			Direction:           direction,
			Amount:              orig.Amount,
			ReferenceType:       ReferenceTypeReversal,
			ReferenceID:         entryID,
		})
		if err != nil {
			return err
		}

		// Reversing a payment allocation voids its receipt: the number
		// and every printed field stay exactly as issued, never reused,
		// never deleted — the PDF renders "VOID" over it instead.
		if orig.ReferenceType == ReferenceTypePaymentAllocation {
			return q.VoidReceipt(ctx, sqlc.VoidReceiptParams{
				TenantID: tenantID, PaymentAllocationID: &orig.ReferenceID, VoidReason: &reason,
			})
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return reversalID, nil
}

// LedgerView is a unit's rent ledger tab: the RENT ledger and the
// RENT_DEPOSIT ledger, each with balance and entries carrying running
// balances (system-design.txt 2).
type LedgerView struct {
	Rent        *UnitLedger `json:"rent"`
	RentDeposit *UnitLedger `json:"rent_deposit"`
}

// GetUnitLedger reads both rent ledgers through the shared LedgerModel, the
// single ledger read path for rent, water and garbage.
func (m RentModel) GetUnitLedger(ctx context.Context, tenantID, unitID uuid.UUID, filters Filters) (*LedgerView, error) {
	ledger := LedgerModel{Store: m.Store, Timeout: m.Timeout}
	rent, err := ledger.GetUnitLedger(ctx, tenantID, unitID, LedgerTypeRent, filters)
	if err != nil {
		return nil, err
	}
	deposit, err := ledger.GetUnitLedger(ctx, tenantID, unitID, LedgerTypeRentDeposit, filters)
	if err != nil {
		return nil, err
	}
	return &LedgerView{Rent: rent, RentDeposit: deposit}, nil
}

// GetPropertyOverview is the Rent tab: every occupied unit's expected, paid
// this period and balance, with a status derived from them.
func (m RentModel) GetPropertyOverview(ctx context.Context, tenantID, propertyID uuid.UUID, period moneyfmt.Period) ([]OverviewRow, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []OverviewRow
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID}); err != nil {
			return notFound(err)
		}
		rows, err := q.ListRentOverview(ctx, sqlc.ListRentOverviewParams{
			Period: period.FirstDay(), TenantID: tenantID, PropertyID: propertyID,
		})
		if err != nil {
			return err
		}
		out = make([]OverviewRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, OverviewRow{
				UnitID: r.UnitID, UnitCode: r.UnitCode, TenantName: r.TenantName,
				Expected: r.Expected, Billed: r.Billed, Paid: r.Paid, Balance: r.Balance,
				Status: rentStatus(r.Balance, r.Paid),
			})
		}
		return nil
	})
	return out, err
}
