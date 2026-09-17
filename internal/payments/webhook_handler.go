package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/pkg/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

type intasendPaymentWebhookPayload struct {
	ID      string `json:"id"`
	ApiRef  string `json:"api_ref"`
	State   string `json:"state"`
	Invoice struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"invoice"`
}

// IntasendWebhook handles the tenant-payment IntaSend webhook (spec §5.5). It
// is mounted at /v1/webhooks/payments/intasend rather than the spec route
// table's literal /v1/webhooks/intasend, because internal/subscriptions
// already owns that exact path for Willcoll's own, unrelated subscription
// billing webhook — the two are different IntaSend accounts/flows and can't
// share a route.
//
// It verifies the signature, looks up payment_gateway_transactions by
// gateway_reference, and on a first-time COMPLETE status posts the matching
// ExecuteRentPaymentTx/ExecuteWaterGarbageTx. This is Willcoll's one
// deliberate exception to "every query runs inside a request-scoped,
// organization-id-set transaction" (spec §1a) — IntaSend is not a logged-in
// Organization user, so organization_id is resolved from the stored
// transaction row instead of a session claim, the same way
// internal/subscriptions' webhook already does.
func (s *Service) IntasendWebhook(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if !s.validSignature(body, r.Header.Get("X-Intasend-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload intasendPaymentWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	reference := payload.ApiRef
	if reference == "" {
		reference = payload.ID
	}
	if reference == "" {
		reference = payload.Invoice.ID
	}
	state := payload.State
	if state == "" {
		state = payload.Invoice.State
	}
	if reference == "" {
		http.Error(w, "missing gateway reference", http.StatusBadRequest)
		return
	}

	newStatus := ""
	switch state {
	case "COMPLETE":
		newStatus = "COMPLETE"
	case "FAILED":
		newStatus = "FAILED"
	default:
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ctx := r.Context()

	var (
		txnID          uuid.UUID
		organizationID uuid.UUID
		unitID         uuid.UUID
		invoiceID      *uuid.UUID
		channel        string
		status         string
	)
	err = s.pool.QueryRow(ctx, `
		SELECT id, organization_id, unit_id, invoice_id, channel, status
		FROM payment_gateway_transactions
		WHERE gateway_provider = 'intasend' AND gateway_reference = $1`,
		reference,
	).Scan(&txnID, &organizationID, &unitID, &invoiceID, &channel, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing we initiated matches this reference; acknowledge so
		// IntaSend doesn't keep retrying a delivery we can never resolve.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(w, "lookup transaction", http.StatusInternalServerError)
		return
	}

	if status != "PENDING" {
		// Already processed by an earlier delivery of this same webhook —
		// the gateway_reference UNIQUE constraint plus this check is the
		// duplicate-delivery guard (spec §5.5).
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if _, err := s.pool.Exec(ctx, `
		UPDATE payment_gateway_transactions
		SET status = $2, raw_payload = $3
		WHERE id = $1`,
		txnID, newStatus, body,
	); err != nil {
		http.Error(w, "update transaction", http.StatusInternalServerError)
		return
	}

	if newStatus != "COMPLETE" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if invoiceID == nil {
		http.Error(w, "transaction has no invoice", http.StatusInternalServerError)
		return
	}

	if err := s.postLedgerEntry(ctx, txnID, organizationID, *invoiceID, channel, reference); err != nil {
		http.Error(w, "post ledger entry", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// postLedgerEntry loads the invoice/tenant/property context for a
// newly-COMPLETE transaction and posts the matching money.Service transfer,
// then links it back onto the payment_gateway_transactions row and sends the
// tenant their SMS receipt.
func (s *Service) postLedgerEntry(ctx context.Context, txnID, organizationID, invoiceID uuid.UUID, channel, gatewayReference string) error {
	var (
		invoiceType string
		tenantID    uuid.UUID
		propertyID  uuid.UUID
		managerID   uuid.UUID
		tenantPhone string
		amount      int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT i.invoice_type::text, l.tenant_id, u.property_id, p.manager_id, t.phone,
		       (pgt.amount * 100)::bigint
		FROM invoices i
		JOIN leases l ON l.id = i.lease_id
		JOIN units u ON u.id = l.unit_id
		JOIN properties p ON p.id = u.property_id
		JOIN tenants t ON t.id = l.tenant_id
		JOIN payment_gateway_transactions pgt ON pgt.id = $2
		WHERE i.id = $1`,
		invoiceID, txnID,
	).Scan(&invoiceType, &tenantID, &propertyID, &managerID, &tenantPhone, &amount)
	if err != nil {
		return fmt.Errorf("lookup payment context: %w", err)
	}

	// Derived from the gateway's own reference (spec §4.3/§5.5), so a
	// re-delivered webhook for the same reference can never double-post even
	// if the transaction-row guard above were somehow bypassed.
	key := idempotency.Derive("intasend", gatewayReference)

	input := money.PaymentTxInput{
		OrganizationID: organizationID,
		TenantID:       tenantID,
		PropertyID:     propertyID,
		InvoiceID:      invoiceID,
		Amount:         amount,
		Method:         ledgerMethodFor(channel),
		Reference:      gatewayReference,
		Narrative:      "IntaSend tenant self-pay",
		IdempotencyKey: key,
		RecordedBy:     managerID,
	}

	var (
		transfer money.Transfer
		txErr    error
	)
	switch invoiceType {
	case "rent":
		transfer, txErr = s.money.ExecuteRentPaymentTx(ctx, input)
	case "water_garbage":
		transfer, txErr = s.money.ExecuteWaterGarbageTx(ctx, input)
	default:
		return fmt.Errorf("unsupported invoice type %q for gateway payment", invoiceType)
	}
	if txErr != nil {
		return fmt.Errorf("post ledger transfer: %w", txErr)
	}

	if _, err := s.pool.Exec(ctx, `
		UPDATE payment_gateway_transactions SET ledger_transfer_id = $2 WHERE id = $1`,
		txnID, transfer.ID,
	); err != nil {
		return fmt.Errorf("link ledger transfer: %w", err)
	}

	if s.notify != nil {
		if err := s.notify.SendTemplate(ctx, organizationID, tenantPhone, "tenant_rent_received.tmpl", map[string]any{
			"Amount": amount,
		}); err != nil {
			// An SMS failure shouldn't fail an already-posted payment; the
			// transfer above already committed.
			_ = err
		}
	}

	return nil
}

func (s *Service) validSignature(body []byte, signature string) bool {
	if s.webhookSecret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
