package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// lockPayments names the per-manager lock every payment posting takes.
const lockPayments = "payments"

// ErrAllocationMismatch is returned when the allocations posted for a payment
// do not add up to the payment amount. It aborts the transaction, so a
// partial or wrong split is never committed (system-design.txt 3.8).
var ErrAllocationMismatch = errors.New("payment allocations do not equal the payment amount")

// PaymentOutcome is how a posted payment is recorded: its status, the intent
// it settled (if any), and whether the engine applied it on a guess a manager
// should confirm, with a note saying why.
type PaymentOutcome struct {
	Status      string
	IntentID    *uuid.UUID
	Unconfirmed bool
	Note        string
}

func (o PaymentOutcome) notePtr() *string {
	if o.Note == "" {
		return nil
	}
	return &o.Note
}

// Allocation is part of a payment applied to one ledger account type.
type Allocation struct {
	LedgerType string
	Amount     moneyfmt.Money
}

// postPaymentAllocations applies a stored payment to a unit's ledger inside
// the caller's (serializable) tenant transaction: one PAYMENT_POSTING header,
// then per allocation a CREDIT entry and its payment_allocations row. It
// asserts that the allocations sum exactly to the payment amount before
// returning (the zero-sum rule), and marks the payment allocated.
//
// It is the single payment posting path: manual entry and PayHero
// reconciliation both go through it. Overpayment is allowed: a CREDIT
// beyond the balance leaves the account in credit, an advance payment.
func postPaymentAllocations(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, payment sqlc.Payment, unitID uuid.UUID, allocations []Allocation, createdBy string, outcome PaymentOutcome) error {
	if len(allocations) == 0 {
		return errors.New("ledger: a payment posting needs at least one allocation")
	}

	headerID, err := q.CreateTransactionHeader(ctx, sqlc.CreateTransactionHeaderParams{
		TenantID:       tenantID,
		Type:           TxTypePaymentPosting,
		IdempotencyKey: "payment:" + payment.ID.String(),
		Description:    "Payment " + payment.MpesaReceipt,
		CreatedBy:      createdBy,
	})
	if err != nil {
		return err
	}

	for _, a := range allocations {
		if !a.Amount.IsPositive() {
			return fmt.Errorf("ledger: allocation to %s must be positive", a.LedgerType)
		}
		accountID, err := q.GetOrCreateLedgerAccount(ctx, sqlc.GetOrCreateLedgerAccountParams{
			TenantID: tenantID, UnitID: unitID, Type: a.LedgerType,
		})
		if err != nil {
			return err
		}

		// The arrears note only makes sense for RENT, and must tell apart
		// genuine carried-over arrears from the current period's own
		// just-posted charge sitting unpaid for a few days: both look like
		// "a positive balance right now," but only a balance that was
		// already positive BEFORE this period's charge existed is arrears.
		// (2026-09-28 regression: every on-time payment was printing
		// "ARREARS CLEARED" because the balance check used the
		// instantaneous pre-credit balance instead of this cutoff.)
		var arrearsNote *string
		var settledPeriod *time.Time
		if a.LedgerType == LedgerTypeRent {
			periodOfPayment := moneyfmt.NewPeriod(payment.ReceivedAt.In(nairobi))
			before, err := q.GetUnitLedgerBalanceAsOf(ctx, sqlc.GetUnitLedgerBalanceAsOfParams{
				TenantID: tenantID, UnitID: unitID, LedgerType: a.LedgerType, AsOf: periodOfPayment.FirstDay(),
			})
			if err != nil {
				return err
			}
			if before.IsPositive() {
				cleared := before
				if a.Amount.Cmp(before) < 0 {
					cleared = a.Amount
				}
				note := "ARREARS CLEARED: KSH. " + cleared.Display()
				arrearsNote = &note

				leases, err := q.ListUnitLeaseRentHistory(ctx, sqlc.ListUnitLeaseRentHistoryParams{TenantID: tenantID, UnitIds: []uuid.UUID{unitID}})
				if err != nil {
					return err
				}
				if owed := arrearsPeriods(periodOfPayment.Prev(), before, leases); len(owed) > 0 {
					oldest := owed[0].FirstDay()
					settledPeriod = &oldest
				}
			}
		}

		allocationID := uuid.New()
		entryID, err := q.CreateLedgerEntry(ctx, sqlc.CreateLedgerEntryParams{
			TenantID:            tenantID,
			TransactionHeaderID: headerID,
			LedgerAccountID:     accountID,
			Direction:           DirectionCredit,
			Amount:              a.Amount,
			ReferenceType:       ReferenceTypePaymentAllocation,
			ReferenceID:         allocationID,
		})
		if err != nil {
			return err
		}
		if err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
			ID: allocationID, TenantID: tenantID, PaymentID: payment.ID,
			LedgerAccountID: accountID, LedgerEntryID: entryID, Amount: a.Amount,
		}); err != nil {
			return err
		}
		if err := issueReceipt(ctx, q, tenantID, unitID, allocationID, a.LedgerType, payment.ReceivedAt, arrearsNote, settledPeriod); err != nil {
			return err
		}
	}

	allocated, err := q.SumPaymentAllocations(ctx, sqlc.SumPaymentAllocationsParams{TenantID: tenantID, PaymentID: payment.ID})
	if err != nil {
		return err
	}
	if allocated != payment.Amount {
		return fmt.Errorf("%w: allocated %s of %s", ErrAllocationMismatch, allocated, payment.Amount)
	}

	return q.SetPaymentMatch(ctx, sqlc.SetPaymentMatchParams{
		TenantID: tenantID, ID: payment.ID, Status: outcome.Status, MatchedUnitID: &unitID,
		MatchedIntentID: outcome.IntentID, AutoAppliedUnconfirmed: outcome.Unconfirmed, ReviewNote: outcome.notePtr(),
	})
}

// nairobi is the timezone every payment date is shown in, same as
// ListPeriodPayments' (received_at AT TIME ZONE 'Africa/Nairobi')::date.
var nairobi = func() *time.Location {
	loc, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		return time.FixedZone("EAT", 3*60*60) // UTC+3, no DST, if the tzdata isn't installed
	}
	return loc
}()

// issueReceipt creates one allocation's receipt: idempotent on
// payment_allocation_id (a retried posting that names the same allocation
// id reuses the existing receipt instead of consuming a new number), and
// numbered from the property's counter under SELECT ... FOR UPDATE, not
// MAX()+1. It runs inside the caller's transaction, so a rollback there
// also rolls back the counter increment — that's what keeps the sequence
// gap-free.
func issueReceipt(ctx context.Context, q sqlc.Querier, tenantID, unitID, allocationID uuid.UUID, ledgerType string, receivedAt time.Time, arrearsNote *string, settledPeriod *time.Time) error {
	if _, err := q.GetReceiptByAllocation(ctx, sqlc.GetReceiptByAllocationParams{
		TenantID: tenantID, PaymentAllocationID: &allocationID,
	}); err == nil {
		return nil // already issued
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID})
	if err != nil {
		return err
	}
	period := moneyfmt.NewPeriod(receivedAt.In(nairobi)).FirstDay()

	no, err := q.LockReceiptCounter(ctx, sqlc.LockReceiptCounterParams{TenantID: tenantID, PropertyID: unit.PropertyID})
	if err != nil {
		return err
	}
	if err := q.AdvanceReceiptCounter(ctx, sqlc.AdvanceReceiptCounterParams{TenantID: tenantID, PropertyID: unit.PropertyID}); err != nil {
		return err
	}
	_, err = q.CreateReceipt(ctx, sqlc.CreateReceiptParams{
		TenantID: tenantID, PropertyID: unit.PropertyID, UnitID: unitID, ReceiptNo: no, Period: period,
		PaymentAllocationID: &allocationID, LedgerType: &ledgerType, ArrearsNote: arrearsNote, SettledPeriod: settledPeriod,
	})
	return err
}

// postingTimeout is the time budget of an operation that posts payments. It
// includes waiting for the per-manager posting lock, which during a burst of
// simultaneous callbacks is legitimately longer than one query, so it is five
// times the ordinary per-call timeout.
func postingTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		d = DefaultQueryTimeout
	}
	return 5 * d
}
