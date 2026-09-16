package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

type createPaymentRequest struct {
	InvoiceID      uuid.UUID `json:"invoice_id"`
	Amount         int64     `json:"amount"` // KES cents
	Method         string    `json:"method"`
	Reference      string    `json:"reference"`
	Narrative      string    `json:"narrative"`
	IdempotencyKey string    `json:"idempotency_key"`
}

type reversePaymentRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

// createPayment handles POST /v1/payments.
func (s *Server) createPayment(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input createPaymentRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var (
		invoiceType string
		tenantID    uuid.UUID
		propertyID  uuid.UUID
		tenantPhone string
		periodMonth time.Time
	)
	err := tx.QueryRow(r.Context(), `
		SELECT i.invoice_type::text, l.tenant_id, u.property_id, t.phone, i.period_month
		FROM invoices i
		JOIN leases l ON l.id = i.lease_id
		JOIN units u ON u.id = l.unit_id
		JOIN tenants t ON t.id = l.tenant_id
		WHERE i.id = $1`,
		input.InvoiceID,
	).Scan(&invoiceType, &tenantID, &propertyID, &tenantPhone, &periodMonth)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup payment invoice", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var transfer money.Transfer
	switch invoiceType {
	case "rent":
		transfer, err = s.money.ExecuteRentPaymentTx(r.Context(), money.PaymentTxInput{
			OrganizationID: claims.OrganizationID,
			TenantID:       tenantID,
			PropertyID:     propertyID,
			InvoiceID:      input.InvoiceID,
			Amount:         input.Amount,
			Method:         input.Method,
			Reference:      input.Reference,
			Narrative:      input.Narrative,
			IdempotencyKey: input.IdempotencyKey,
			RecordedBy:     claims.UserID,
		})
	case "water_garbage":
		transfer, err = s.money.ExecuteWaterGarbageTx(r.Context(), money.PaymentTxInput{
			OrganizationID: claims.OrganizationID,
			TenantID:       tenantID,
			PropertyID:     propertyID,
			InvoiceID:      input.InvoiceID,
			Amount:         input.Amount,
			Method:         input.Method,
			Reference:      input.Reference,
			Narrative:      input.Narrative,
			IdempotencyKey: input.IdempotencyKey,
			RecordedBy:     claims.UserID,
		})
	default:
		writeJSONError(w, http.StatusUnprocessableEntity, "invoice is not a rent or water_garbage invoice")
		return
	}
	if err != nil {
		s.logger.Error("record payment", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if s.notify != nil {
		if err := s.notify.SendTemplate(r.Context(), claims.OrganizationID, tenantPhone, "tenant_rent_received.tmpl", map[string]any{
			"Amount": input.Amount,
			"Period": periodMonth.Format("2006-01"),
		}); err != nil {
			s.logger.Error("send receipt sms", "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, transfer, "data")
}

// reversePayment handles POST /v1/payments/:id/reverse.
func (s *Server) reversePayment(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	transferID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payment id")
		return
	}

	var input reversePaymentRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, _ := claimsFromContext(r.Context())

	transfer, err := s.money.ReverseTransferTx(r.Context(), money.ReverseTxInput{
		OrganizationID: claims.OrganizationID,
		TransferID:     transferID,
		IdempotencyKey: input.IdempotencyKey,
		RecordedBy:     claims.UserID,
	})
	if err != nil {
		s.logger.Error("reverse payment", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, transfer, "data")
}
