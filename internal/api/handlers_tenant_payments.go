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

type submitPayoutAccountRequest struct {
	BankName             string `json:"bank_name"`
	BankBranchCode       string `json:"bank_branch_code"`
	AccountName          string `json:"account_name"`
	AccountNumber        string `json:"account_number"`
	KRAPin               string `json:"kra_pin"`
	BusinessDocType      string `json:"business_doc_type"`
	BusinessDocReference string `json:"business_doc_reference"`
}

func payoutAccountResponse(a payments.PayoutAccount) map[string]any {
	return map[string]any{
		"id":                     a.ID,
		"bank_name":              a.BankName,
		"bank_branch_code":       a.BankBranchCode,
		"account_name":           a.AccountName,
		"account_number":         a.AccountNumber,
		"kra_pin":                a.KRAPin,
		"business_doc_type":      a.BusinessDocType,
		"business_doc_reference": a.BusinessDocReference,
		"gateway_subaccount_id":  a.GatewaySubaccountID,
		"kyc_status":             a.KYCStatus,
	}
}

// submitPayoutAccount handles POST /v1/managers/payout-account — Manager
// only (spec §2.4).
func (s *Server) submitPayoutAccount(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input submitPayoutAccountRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	account, err := s.payments.SubmitPayoutAccount(r.Context(), payments.SubmitPayoutAccountInput{
		OrganizationID:       claims.OrganizationID,
		ManagerID:            claims.UserID,
		BankName:             input.BankName,
		BankBranchCode:       input.BankBranchCode,
		AccountName:          input.AccountName,
		AccountNumber:        input.AccountNumber,
		KRAPin:               input.KRAPin,
		BusinessDocType:      input.BusinessDocType,
		BusinessDocReference: input.BusinessDocReference,
	})
	switch {
	case errors.Is(err, payments.ErrPayoutFieldsRequired), errors.Is(err, payments.ErrPayoutDocTypeInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		s.logger.Error("submit payout account", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, payoutAccountResponse(account), "data")
}

// getPayoutAccount handles GET /v1/managers/payout-account — Manager only
// (spec §2.4).
func (s *Server) getPayoutAccount(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	account, err := s.payments.GetPayoutAccount(r.Context(), claims.UserID)
	if errors.Is(err, payments.ErrPayoutAccountNotFound) {
		writeJSONError(w, http.StatusNotFound, "no payout account has been submitted yet")
		return
	}
	if err != nil {
		s.logger.Error("get payout account", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, payoutAccountResponse(account), "data")
}
