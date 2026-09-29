package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
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
)

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
