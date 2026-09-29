package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Payment sources and statuses (system-design.txt 3.5).
const (
	PaymentSourcePayheroSTK = "payhero_stk"
	PaymentSourcePayheroC2B = "payhero_c2b"
	PaymentSourceManual     = "manual"

	PaymentStatusUnmatched = "unmatched"
	PaymentStatusMatched   = "matched"
	PaymentStatusAllocated = "allocated"
)

// PaymentIntentStatus values (system-design.txt 3.5).
const (
	PaymentIntentStatusPending   = "pending"
	PaymentIntentStatusCompleted = "completed"
	PaymentIntentStatusExpired   = "expired"
	PaymentIntentStatusFailed    = "failed"
)

// PaymentIntent is created the instant a tenant submits the PayIntentForm.
type PaymentIntent struct {
	ID                uuid.UUID       `json:"id"`
	TenantID          uuid.UUID       `json:"-"`
	UnitID            uuid.UUID       `json:"unit_id"`
	Lines             json.RawMessage `json:"lines"`
	ExternalReference string          `json:"external_reference"`
	Phone             string          `json:"phone"`
	Status            string          `json:"status"`
	CreatedAt         time.Time       `json:"created_at"`
	ExpiresAt         time.Time       `json:"expires_at"`
}

// Payment is raw inbound money, one row per webhook delivery.
// mpesa_receipt is the primary idempotency key (system-design.txt 3.8).
type Payment struct {
	ID               uuid.UUID       `json:"id"`
	TenantID         uuid.UUID       `json:"-"`
	Source           string          `json:"source"`
	MpesaReceipt     string          `json:"mpesa_receipt"`
	PayheroReference *string         `json:"payhero_reference,omitempty"`
	Amount           moneyfmt.Money  `json:"amount"`
	Msisdn           string          `json:"msisdn"`
	PayerName        *string         `json:"payer_name,omitempty"`
	AccountReference *string         `json:"account_reference,omitempty"`
	MatchedUnitID    *uuid.UUID      `json:"matched_unit_id,omitempty"`
	MatchedIntentID  *uuid.UUID      `json:"matched_intent_id,omitempty"`
	Status           string          `json:"status"`
	RawPayload       json.RawMessage `json:"-"`
	ReceivedAt       time.Time       `json:"received_at"`
	CreatedAt        time.Time       `json:"created_at"`
}

// PaymentModel is the service layer for payment intents and payments. It
// has no Update or Delete: payments are recorded, and the money they carry
// moves through the append-only ledger (payment_allocations +
// ledger_entries), never by editing rows.
type PaymentModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// InsertIntent records intent for intent.TenantID.
func (m PaymentModel) InsertIntent(ctx context.Context, intent *PaymentIntent) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, intent.TenantID, func(q sqlc.Querier) error {
		row, err := q.CreatePaymentIntent(ctx, sqlc.CreatePaymentIntentParams{
			TenantID:          intent.TenantID,
			UnitID:            intent.UnitID,
			Lines:             intent.Lines,
			ExternalReference: intent.ExternalReference,
			Phone:             intent.Phone,
			Status:            intent.Status,
			ExpiresAt:         intent.ExpiresAt,
		})
		if err != nil {
			return err
		}

		*intent = paymentIntentFromRow(row)
		return nil
	})
}

// GetIntent returns the tenant's payment intent, or ErrRecordNotFound.
func (m PaymentModel) GetIntent(ctx context.Context, tenantID, id uuid.UUID) (*PaymentIntent, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var intent PaymentIntent

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetPaymentIntent(ctx, sqlc.GetPaymentIntentParams{TenantID: tenantID, ID: id})
		if err != nil {
			return notFound(err)
		}

		intent = paymentIntentFromRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &intent, nil
}

// Insert records an inbound payment idempotently: a duplicate
// mpesa_receipt (PayHero retrying a delivery) inserts nothing and reports
// inserted == false with a nil error, so webhook handlers can always ack
// 200 (system-design.txt 3.8).
func (m PaymentModel) Insert(ctx context.Context, payment *Payment) (inserted bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	err = m.Store.ExecTenantTx(ctx, payment.TenantID, func(q sqlc.Querier) error {
		row, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
			TenantID:         payment.TenantID,
			Source:           payment.Source,
			MpesaReceipt:     payment.MpesaReceipt,
			PayheroReference: payment.PayheroReference,
			Amount:           payment.Amount,
			Msisdn:           payment.Msisdn,
			PayerName:        payment.PayerName,
			AccountReference: payment.AccountReference,
			Status:           payment.Status,
			RawPayload:       payment.RawPayload,
			ReceivedAt:       payment.ReceivedAt,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				inserted = false
				return nil
			}
			return err
		}

		*payment = paymentFromRow(row)
		inserted = true
		return nil
	})
	return inserted, err
}

// GetByMpesaReceipt returns the tenant's payment with the given receipt,
// or ErrRecordNotFound.
func (m PaymentModel) GetByMpesaReceipt(ctx context.Context, tenantID uuid.UUID, mpesaReceipt string) (*Payment, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var payment Payment

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetPaymentByMpesaReceipt(ctx, sqlc.GetPaymentByMpesaReceiptParams{
			TenantID: tenantID, MpesaReceipt: mpesaReceipt,
		})
		if err != nil {
			return notFound(err)
		}

		payment = paymentFromRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &payment, nil
}

func paymentIntentFromRow(row sqlc.PaymentIntent) PaymentIntent {
	return PaymentIntent{
		ID:                row.ID,
		TenantID:          row.TenantID,
		UnitID:            row.UnitID,
		Lines:             row.Lines,
		ExternalReference: row.ExternalReference,
		Phone:             row.Phone,
		Status:            row.Status,
		CreatedAt:         row.CreatedAt,
		ExpiresAt:         row.ExpiresAt,
	}
}

func paymentFromRow(row sqlc.Payment) Payment {
	return Payment{
		ID:               row.ID,
		TenantID:         row.TenantID,
		Source:           row.Source,
		MpesaReceipt:     row.MpesaReceipt,
		PayheroReference: row.PayheroReference,
		Amount:           row.Amount,
		Msisdn:           row.Msisdn,
		PayerName:        row.PayerName,
		AccountReference: row.AccountReference,
		MatchedUnitID:    row.MatchedUnitID,
		MatchedIntentID:  row.MatchedIntentID,
		Status:           row.Status,
		RawPayload:       row.RawPayload,
		ReceivedAt:       row.ReceivedAt,
		CreatedAt:        row.CreatedAt,
	}
}
