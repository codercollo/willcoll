package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

var (
	// ErrPaymentAlreadyAllocated is returned when a manager tries to place a
	// payment that already has allocations. A wrong placement is corrected
	// by reversing its ledger entries, not by placing the payment again.
	ErrPaymentAlreadyAllocated = errors.New("payment is already allocated")

	// ErrNothingToConfirm is returned when a payment is not awaiting
	// confirmation.
	ErrNothingToConfirm = errors.New("payment has nothing to confirm")

	// ErrSelfConfirm is returned when the person who recorded a pending payment
	// tries to confirm it. Whoever types "cash received" is never the one who
	// accepts it, whatever role they hold by then (a promoted officer included).
	ErrSelfConfirm = errors.New("a payment cannot be confirmed by the person who recorded it")
)

// StalePendingAfter is how long a hand-entered payment may wait for a decision
// before the dashboard counts it as overdue.
const StalePendingAfter = 48 * time.Hour

// ReviewPayment is one payment in the manager review queue.
type ReviewPayment struct {
	ID                     uuid.UUID      `json:"id"`
	Source                 string         `json:"source"`
	MpesaReceipt           string         `json:"mpesa_receipt"`
	Amount                 moneyfmt.Money `json:"amount"`
	Msisdn                 string         `json:"msisdn"`
	PayerName              *string        `json:"payer_name,omitempty"`
	AccountReference       *string        `json:"account_reference,omitempty"`
	MatchedUnitID          *uuid.UUID     `json:"matched_unit_id,omitempty"`
	UnitCode               *string        `json:"unit_code,omitempty"`
	Status                 string         `json:"status"`
	AutoAppliedUnconfirmed bool           `json:"auto_applied_unconfirmed"`
	ReviewNote             *string        `json:"review_note,omitempty"`
	ReceivedAt             time.Time      `json:"received_at"`

	// A pending hand-entered payment: the ledger it will credit, who recorded it
	// (an id and the role they held; never a name or phone) and when it was
	// entered. Nil on every other payment.
	PendingLedgerType *string    `json:"pending_ledger_type,omitempty"`
	RecordedBy        *uuid.UUID `json:"recorded_by,omitempty"`
	RecordedByRole    *string    `json:"recorded_by_role,omitempty"`
	RecordedAt        time.Time  `json:"recorded_at"`
}

// PendingSummary counts the hand-entered payments waiting for a decision, and
// how many of them have waited longer than StalePendingAfter. The dashboard shows
// the second number.
type PendingSummary struct {
	Pending      int `json:"pending"`
	PendingStale int `json:"pending_over_48h"`
}

// PendingSummary reports the pending counts for one firm.
func (m PaymentModel) PendingSummary(ctx context.Context, tenantID uuid.UUID) (PendingSummary, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out PendingSummary
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		pending, err := q.CountPendingPayments(ctx, tenantID)
		if err != nil {
			return err
		}
		stale, err := q.CountStalePendingPayments(ctx, sqlc.CountStalePendingPaymentsParams{
			TenantID: tenantID, OlderThan: moneyfmt.Now().Add(-StalePendingAfter),
		})
		if err != nil {
			return err
		}
		out = PendingSummary{Pending: int(pending), PendingStale: int(stale)}
		return nil
	})
	return out, err
}

// ListForReview pages the payments a manager still has to look at: unmatched,
// matched-but-unplaced, and auto-applied-unconfirmed, newest first.
func (m PaymentModel) ListForReview(ctx context.Context, tenantID uuid.UUID, filters Filters) ([]*ReviewPayment, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []*ReviewPayment
	total := 0
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListPaymentsForReview(ctx, sqlc.ListPaymentsForReviewParams{
			TenantID: tenantID, PageLimit: int32(filters.limit()), PageOffset: int32(filters.offset()),
		})
		if err != nil {
			return err
		}
		out, total = make([]*ReviewPayment, 0, len(rows)), 0
		for _, r := range rows {
			total = int(r.TotalRecords)
			out = append(out, &ReviewPayment{
				ID: r.ID, Source: r.Source, MpesaReceipt: r.MpesaReceipt, Amount: r.Amount, Msisdn: r.Msisdn,
				PayerName: r.PayerName, AccountReference: r.AccountReference, MatchedUnitID: r.MatchedUnitID,
				UnitCode: r.UnitCode, Status: r.Status, AutoAppliedUnconfirmed: r.AutoAppliedUnconfirmed,
				ReviewNote: r.ReviewNote, ReceivedAt: r.ReceivedAt,
				PendingLedgerType: r.PendingLedgerType, RecordedBy: r.RecordedBy, RecordedByRole: r.RecordedByRole,
				RecordedAt: r.CreatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, Metadata{}, err
	}
	return out, calculateMetadata(total, filters.Page, filters.PageSize), nil
}

// MatchAndAllocate is how a manager places a payment the engine could not:
// it applies the payment to the unit's ledgers in one serializable
// transaction (retried on serialization failure), asserting before commit
// that the allocations add up to exactly the payment amount. Only a payment
// with no allocations can be placed; the posting is attributed to the manager.
func (m PaymentModel) MatchAndAllocate(ctx context.Context, tenantID, managerID, paymentID, unitID uuid.UUID, allocations []Allocation) error {
	ctx, cancel := withTimeout(ctx, postingTimeout(m.Timeout))
	defer cancel()

	if len(allocations) == 0 {
		return fmt.Errorf("%w: at least one allocation is required", ErrInvalidInput)
	}
	for _, a := range allocations {
		if !manualLedgerTypes[a.LedgerType] {
			return fmt.Errorf("%w: unknown ledger type %q", ErrInvalidInput, a.LedgerType)
		}
	}

	return m.Store.ExecTenantTxExclusive(ctx, tenantID, lockPayments, func(q sqlc.Querier) error {
		payment, err := q.GetPayment(ctx, sqlc.GetPaymentParams{TenantID: tenantID, ID: paymentID})
		if err != nil {
			return notFound(err)
		}
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}
		already, err := q.SumPaymentAllocations(ctx, sqlc.SumPaymentAllocationsParams{TenantID: tenantID, PaymentID: paymentID})
		if err != nil {
			return err
		}
		if already.IsPositive() {
			return ErrPaymentAlreadyAllocated
		}

		var sum moneyfmt.Money
		for _, a := range allocations {
			sum = sum.Add(a.Amount)
		}
		if sum != payment.Amount {
			return fmt.Errorf("%w: the allocations total %s but the payment is %s", ErrInvalidInput, sum, payment.Amount)
		}

		return postPaymentAllocations(ctx, q, tenantID, payment, unitID, allocations, managerID.String(),
			PaymentOutcome{Status: PaymentStatusAllocated, IntentID: payment.MatchedIntentID})
	})
}

// Confirm clears the unconfirmed flag on a payment the engine applied on its
// own best guess, once a manager has checked it.
func (m PaymentModel) Confirm(ctx context.Context, tenantID, paymentID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetPayment(ctx, sqlc.GetPaymentParams{TenantID: tenantID, ID: paymentID}); err != nil {
			return notFound(err)
		}
		n, err := q.ConfirmPayment(ctx, sqlc.ConfirmPaymentParams{TenantID: tenantID, ID: paymentID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNothingToConfirm
		}
		return nil
	})
}

// PaymentDecision is what a confirmation or rejection acted on, for the audit
// trail and the owner alert: it never carries a name or a phone number.
type PaymentDecision struct {
	PaymentID      uuid.UUID
	Pending        bool // a hand-entered payment that was waiting (as opposed to an engine guess)
	Amount         moneyfmt.Money
	UnitID         *uuid.UUID
	Ledger         string
	RecordedBy     *uuid.UUID
	RecordedByRole string
}

// PaymentHook runs inside the transaction of a rejection, after its writes, so an
// owner alert commits with the decision or not at all. An error rolls it back.
type PaymentHook func(ctx context.Context, q sqlc.Querier, d PaymentDecision) error

func decisionOf(p sqlc.Payment) PaymentDecision {
	d := PaymentDecision{PaymentID: p.ID, Pending: p.Status == PaymentStatusPending, Amount: p.Amount, UnitID: p.MatchedUnitID, RecordedBy: p.RecordedBy}
	if p.PendingLedgerType != nil {
		d.Ledger = *p.PendingLedgerType
	}
	if p.RecordedByRole != nil {
		d.RecordedByRole = *p.RecordedByRole
	}
	return d
}

// ConfirmAs is a manager's confirmation of a payment waiting on them, by
// confirmerID (the signed-in principal's id).
//
//   - A pending hand-entered payment is POSTED now, to the unit and ledger it was
//     recorded against, through the one posting path (receipt included), and
//     marked confirmed by and at. Confirming twice posts once: the second call
//     finds nothing pending and returns ErrNothingToConfirm. The person who
//     recorded it can never confirm it (ErrSelfConfirm), whatever role they hold
//     now.
//   - Any other payment is an engine guess: its unconfirmed flag is cleared, as
//     Confirm does.
func (m PaymentModel) ConfirmAs(ctx context.Context, tenantID, confirmerID, paymentID uuid.UUID) (PaymentDecision, error) {
	ctx, cancel := withTimeout(ctx, postingTimeout(m.Timeout))
	defer cancel()

	var decision PaymentDecision
	err := m.Store.ExecTenantTxExclusive(ctx, tenantID, lockPayments, func(q sqlc.Querier) error {
		payment, err := q.GetPayment(ctx, sqlc.GetPaymentParams{TenantID: tenantID, ID: paymentID})
		if err != nil {
			return notFound(err)
		}
		decision = decisionOf(payment)

		if payment.Status != PaymentStatusPending {
			n, err := q.ConfirmPayment(ctx, sqlc.ConfirmPaymentParams{TenantID: tenantID, ID: paymentID})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNothingToConfirm
			}
			return nil
		}

		if payment.RecordedBy != nil && *payment.RecordedBy == confirmerID {
			return ErrSelfConfirm
		}
		if payment.MatchedUnitID == nil || payment.PendingLedgerType == nil {
			return fmt.Errorf("payment %s is pending but has no unit or ledger to post to", paymentID)
		}
		if err := postPaymentAllocations(ctx, q, tenantID, payment, *payment.MatchedUnitID,
			[]Allocation{{LedgerType: *payment.PendingLedgerType, Amount: payment.Amount}}, confirmerID.String(),
			PaymentOutcome{Status: PaymentStatusAllocated}); err != nil {
			return err
		}
		return q.MarkPaymentConfirmed(ctx, sqlc.MarkPaymentConfirmedParams{TenantID: tenantID, ID: paymentID, ConfirmedBy: &confirmerID})
	})
	return decision, err
}

// MaxRejectReasonLen bounds a rejection reason.
const MaxRejectReasonLen = 500

// RejectPending refuses a pending hand-entered payment with a reason. Nothing
// posts: no ledger entry, balance, report or receipt is ever created for it. It
// stays in the table as the record of what was refused, and the owner is alerted
// by hooks, in the same transaction. A payment that is not pending (already
// confirmed or rejected, or an engine payment) is ErrNothingToConfirm.
func (m PaymentModel) RejectPending(ctx context.Context, tenantID, rejecterID, paymentID uuid.UUID, reason string, hooks ...PaymentHook) (PaymentDecision, error) {
	ctx, cancel := withTimeout(ctx, postingTimeout(m.Timeout))
	defer cancel()

	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return PaymentDecision{}, ErrReasonRequired
	case len(reason) > MaxRejectReasonLen:
		return PaymentDecision{}, fmt.Errorf("%w: the reason is too long", ErrInvalidInput)
	}

	var decision PaymentDecision
	err := m.Store.ExecTenantTxExclusive(ctx, tenantID, lockPayments, func(q sqlc.Querier) error {
		payment, err := q.GetPayment(ctx, sqlc.GetPaymentParams{TenantID: tenantID, ID: paymentID})
		if err != nil {
			return notFound(err)
		}
		decision = decisionOf(payment)
		n, err := q.RejectPendingPayment(ctx, sqlc.RejectPendingPaymentParams{
			TenantID: tenantID, ID: paymentID, RejectedBy: &rejecterID, RejectReason: &reason,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNothingToConfirm
		}
		for _, h := range hooks {
			if err := h(ctx, q, decision); err != nil {
				return err
			}
		}
		return nil
	})
	return decision, err
}
