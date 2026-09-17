// Package payments handles tenant self-pay via IntaSend — M-Pesa STK push
// and hosted card checkout (spec §5.5). Willcoll runs one IntaSend merchant
// account; each Manager is provisioned as an IntaSend sub-account once they
// complete payout onboarding (§2.4, §3.1b) by submitting their own bank
// details and KYC docs — Willcoll never sees or stores a landlord/Manager's
// own M-Pesa till/paybill number or API keys, and IntaSend settles to the
// Manager's linked bank account on its own payout schedule.
package payments

import (
	"context"
	"errors"
	"fmt"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/notify"
	"github.com/codercollo/willcoll-sys/pkg/intasendclient"
	"github.com/codercollo/willcoll-sys/pkg/kesmoney"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Gateway is the consumer-defined seam for creating an IntaSend checkout,
// satisfied by pkg/intasendclient.Client.
type Gateway interface {
	CreateCheckout(ctx context.Context, req intasendclient.CheckoutRequest) (intasendclient.CheckoutResponse, error)
}

// Service handles tenant self-pay via IntaSend: initiating a checkout, and —
// via its webhook — posting the matching ledger transfer through
// internal/money once IntaSend confirms COMPLETE. It never writes ledger
// tables directly; internal/money is still the only code allowed to do that
// (spec §4.3).
type Service struct {
	pool          *pgxpool.Pool
	gateway       Gateway
	money         *money.Service
	notify        *notify.Service
	webhookSecret string
}

// NewService constructs a payments Service. webhookSecret is the shared
// secret IntaSend signs its webhook payloads with (INTASEND_WEBHOOK_SECRET).
func NewService(pool *pgxpool.Pool, gateway Gateway, moneyService *money.Service, notifyService *notify.Service, webhookSecret string) *Service {
	return &Service{pool: pool, gateway: gateway, money: moneyService, notify: notifyService, webhookSecret: webhookSecret}
}

var (
	ErrInvoiceNotFound    = errors.New("invoice not found")
	ErrInvoiceAlreadyPaid = errors.New("invoice is already fully paid")
	ErrPayoutNotVerified  = errors.New("the property's manager has not completed payout onboarding")
	ErrUnsupportedMethod  = errors.New("unsupported payment method")
)

// InitiateInput carries a tenant-initiated checkout request. There is no
// login on this endpoint (spec §5.5) — invoice_id is an unguessable UUID
// delivered only via the tenant's own SMS payment link/USSD prompt, which is
// the "signature" this flow relies on: nobody else knows it.
type InitiateInput struct {
	InvoiceID   uuid.UUID
	Method      string // "M-PESA" | "CARD-PAYMENT"
	RedirectURL string // required for CARD-PAYMENT; ignored for M-PESA
}

// InitiateResult is what the tenant's browser/USSD flow needs next.
type InitiateResult struct {
	TransactionID uuid.UUID
	CheckoutURL   string
}

// channelFor maps the caller's requested IntaSend method onto the
// payment_gateway_transactions.channel column.
func channelFor(method string) (string, error) {
	switch method {
	case "M-PESA":
		return "mpesa", nil
	case "CARD-PAYMENT":
		return "card", nil
	default:
		return "", ErrUnsupportedMethod
	}
}

// ledgerMethodFor maps a stored channel onto the payment_method ledger enum
// value reserved for gateway-collected payments (intasend_mpesa/intasend_card,
// as opposed to the manual cash/mpesa_manual/bank/cheque methods a Manager
// records by hand).
func ledgerMethodFor(channel string) string {
	if channel == "card" {
		return "intasend_card"
	}
	return "intasend_mpesa"
}

// InitiatePayment creates a PENDING payment_gateway_transactions row and
// starts an IntaSend checkout for the remaining balance on input.InvoiceID,
// tagged to the property's Manager's verified sub-account so IntaSend knows
// who to eventually settle to.
func (s *Service) InitiatePayment(ctx context.Context, input InitiateInput) (InitiateResult, error) {
	channel, err := channelFor(input.Method)
	if err != nil {
		return InitiateResult{}, err
	}

	var (
		organizationID          uuid.UUID
		unitID                  uuid.UUID
		managerID               uuid.UUID
		tenantPhone, tenantName string
		amountDue, amountPaid   int64
	)
	err = s.pool.QueryRow(ctx, `
		SELECT i.organization_id, u.id, p.manager_id, t.phone, t.full_name,
		       (i.amount_due * 100)::bigint, (i.amount_paid * 100)::bigint
		FROM invoices i
		JOIN leases l ON l.id = i.lease_id
		JOIN units u ON u.id = l.unit_id
		JOIN properties p ON p.id = u.property_id
		JOIN tenants t ON t.id = l.tenant_id
		WHERE i.id = $1`,
		input.InvoiceID,
	).Scan(&organizationID, &unitID, &managerID, &tenantPhone, &tenantName, &amountDue, &amountPaid)
	if errors.Is(err, pgx.ErrNoRows) {
		return InitiateResult{}, ErrInvoiceNotFound
	}
	if err != nil {
		return InitiateResult{}, fmt.Errorf("lookup invoice: %w", err)
	}

	remaining := amountDue - amountPaid
	if remaining <= 0 {
		return InitiateResult{}, ErrInvoiceAlreadyPaid
	}

	var subaccountID string
	err = s.pool.QueryRow(ctx, `
		SELECT gateway_subaccount_id
		FROM manager_payout_accounts
		WHERE manager_id = $1 AND kyc_status = 'verified' AND gateway_subaccount_id IS NOT NULL`,
		managerID,
	).Scan(&subaccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return InitiateResult{}, ErrPayoutNotVerified
	}
	if err != nil {
		return InitiateResult{}, fmt.Errorf("lookup payout account: %w", err)
	}

	transactionID := uuid.New()
	// apiRef is our own generated reference, sent to IntaSend as api_ref and
	// echoed back on the webhook. payment_gateway_transactions.gateway_reference
	// is keyed on this rather than IntaSend's own checkout id, since this is
	// known before IntaSend ever responds (spec §5.5: "a client-generated
	// idempotency reference in the metadata").
	apiRef := transactionID.String()

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO payment_gateway_transactions (
			id, organization_id, gateway_provider, gateway_reference, gateway_subaccount_id,
			unit_id, invoice_id, amount, currency, channel, status, raw_payload
		)
		VALUES ($1, $2, 'intasend', $3, $4, $5, $6, $7::numeric, 'KES', $8, 'PENDING', '{}'::jsonb)`,
		transactionID, organizationID, apiRef, subaccountID, unitID, input.InvoiceID,
		kesString(remaining), channel,
	); err != nil {
		return InitiateResult{}, fmt.Errorf("create pending transaction: %w", err)
	}

	checkout, err := s.gateway.CreateCheckout(ctx, intasendclient.CheckoutRequest{
		Currency:    "KES",
		Amount:      kesString(remaining),
		PhoneNumber: tenantPhone,
		Method:      input.Method,
		ApiRef:      apiRef,
		Name:        tenantName,
		RedirectURL: input.RedirectURL,
	})
	if err != nil {
		if _, updateErr := s.pool.Exec(ctx, `
			UPDATE payment_gateway_transactions SET status = 'FAILED' WHERE id = $1`,
			transactionID,
		); updateErr != nil {
			return InitiateResult{}, fmt.Errorf("create intasend checkout: %w (and mark failed: %v)", err, updateErr)
		}
		return InitiateResult{}, fmt.Errorf("create intasend checkout: %w", err)
	}

	return InitiateResult{TransactionID: transactionID, CheckoutURL: checkout.URL}, nil
}

func kesString(cents int64) string {
	return kesmoney.FromCents(cents).String()
}
