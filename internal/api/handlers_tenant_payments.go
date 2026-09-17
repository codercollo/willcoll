package api

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll-sys/internal/payments"
	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
)

type initiatePaymentRequest struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	Method      string    `json:"method"` // "M-PESA" | "CARD-PAYMENT"
	RedirectURL string    `json:"redirect_url"`
}

// initiatePayment handles POST /v1/payments/initiate — tenant-facing, no
// login (spec §5.5). The invoice_id is the only credential this endpoint
// checks: it's an unguessable UUID that only reaches the tenant via their
// own SMS payment link.
func (s *Server) initiatePayment(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input initiatePaymentRequest
	if !readJSON(w, r, &input) {
		return
	}

	if input.InvoiceID == uuid.Nil {
		writeJSONError(w, http.StatusBadRequest, "invoice_id is required")
		return
	}

	result, err := s.payments.InitiatePayment(r.Context(), payments.InitiateInput{
		InvoiceID:   input.InvoiceID,
		Method:      input.Method,
		RedirectURL: input.RedirectURL,
	})
	switch {
	case errors.Is(err, payments.ErrInvoiceNotFound):
		writeJSONError(w, http.StatusNotFound, "invoice not found")
		return
	case errors.Is(err, payments.ErrInvoiceAlreadyPaid):
		writeJSONError(w, http.StatusConflict, "invoice is already fully paid")
		return
	case errors.Is(err, payments.ErrPayoutNotVerified):
		writeJSONError(w, http.StatusUnprocessableEntity, "this property's manager has not completed payout onboarding yet")
		return
	case errors.Is(err, payments.ErrUnsupportedMethod):
		writeJSONError(w, http.StatusBadRequest, "method must be M-PESA or CARD-PAYMENT")
		return
	case err != nil:
		s.logger.Error("initiate payment", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"transaction_id": result.TransactionID,
		"checkout_url":   result.CheckoutURL,
	}, "data")
}
