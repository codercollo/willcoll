package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/pkg/idempotency"
	"github.com/codercollo/willcoll-sys/pkg/kesmoney"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/julienschmidt/httprouter"
)

type createPaymentRequest struct {
	// Legacy single-invoice mode.
	InvoiceID      uuid.UUID `json:"invoice_id"`
	Amount         int64     `json:"amount"` // KES cents
	Method         string    `json:"method"`
	Reference      string    `json:"reference"`
	Narrative      string    `json:"narrative"`
	IdempotencyKey string    `json:"idempotency_key"`

	// Manual Payment Recording mode: a Manager records cash/M-Pesa/bank/card
	// the tenant already paid outside the app, applied across a unit's open
	// invoices (Manual Payment Recording brief, phase 3.1). Selected by
	// unit_id being set instead of invoice_id.
	UnitID          uuid.UUID `json:"unit_id"`
	PaymentMethod   string    `json:"payment_method"` // "cash" | "mpesa" | "bank" | "card"
	ReferenceNumber string    `json:"reference_number"`
	PhotoURL        string    `json:"photo_url"`
	Notes           string    `json:"notes"`
}

type reversePaymentRequest struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

// createPayment handles POST /v1/payments. unit_id selects the Manual
// Payment Recording allocation path (phase 3.1); otherwise it falls back to
// the legacy single-invoice mode.
func (s *Server) createPayment(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input createPaymentRequest
	if !readJSON(w, r, &input) {
		return
	}

	if input.UnitID != uuid.Nil {
		s.recordManualPayment(w, r, input)
		return
	}

	if err := idempotency.Validate(input.IdempotencyKey); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
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

type allocationLegResponse struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	InvoiceType string    `json:"invoice_type"`
	PeriodMonth string    `json:"period_month"`
	Applied     int64     `json:"applied"` // KES cents
	TransferID  uuid.UUID `json:"transfer_id"`
}

type manualPaymentResponse struct {
	UnitID            uuid.UUID               `json:"unit_id"`
	TotalAmount       int64                   `json:"total_amount"` // KES cents
	Allocations       []allocationLegResponse `json:"allocations"`
	UnallocatedAmount int64                   `json:"unallocated_amount"` // KES cents; overpayment/credit, never dropped
}

// recordManualPayment is the Manual Payment Recording path of POST
// /v1/payments (phase 3): a Manager records cash/M-Pesa/bank/card a tenant
// already paid outside the app, split across the unit's open invoices by
// money.Service.AllocateManualPayment — purpose is never typed freely here,
// only reference_number/method/photo/notes are (phase 3.2).
func (s *Server) recordManualPayment(w http.ResponseWriter, r *http.Request, input createPaymentRequest) {
	if input.Amount <= 0 {
		writeJSONError(w, http.StatusBadRequest, "amount must be greater than zero")
		return
	}
	if strings.TrimSpace(input.ReferenceNumber) == "" {
		writeJSONError(w, http.StatusBadRequest, "reference_number is required")
		return
	}

	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	result, err := s.money.AllocateManualPayment(r.Context(), money.ManualPaymentInput{
		OrganizationID:  claims.OrganizationID,
		UnitID:          input.UnitID,
		Amount:          input.Amount,
		Method:          input.PaymentMethod,
		ReferenceNumber: input.ReferenceNumber,
		ReceiptPhotoURL: input.PhotoURL,
		RecordedBy:      claims.UserID,
	})
	switch {
	case errors.Is(err, money.ErrManualPaymentAmountInvalid), errors.Is(err, money.ErrManualPaymentMethodInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, money.ErrDuplicateReferenceNumber):
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		s.logger.Error("allocate manual payment", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	response := manualPaymentResponse{
		UnitID:            result.UnitID,
		TotalAmount:       result.TotalAmount,
		UnallocatedAmount: result.UnallocatedAmount,
		Allocations:       make([]allocationLegResponse, 0, len(result.Allocations)),
	}
	for _, leg := range result.Allocations {
		response.Allocations = append(response.Allocations, allocationLegResponse{
			InvoiceID:   leg.InvoiceID,
			InvoiceType: leg.InvoiceType,
			PeriodMonth: leg.PeriodMonth.Format("2006-01"),
			Applied:     leg.Applied,
			TransferID:  leg.Transfer.ID,
		})
	}

	s.auditManualPayment(r.Context(), claims, input, result)
	s.smsManualPaymentReceipt(r.Context(), claims.OrganizationID, input.UnitID, input.ReferenceNumber, result)

	writeJSON(w, http.StatusCreated, response, "data")
}

// auditManualPayment writes the append-only audit trail entry required by
// phase 3.4 (who, when, unit, amount, method, reference). A failure here is
// logged, not surfaced — the payment itself already posted successfully.
func (s *Server) auditManualPayment(ctx context.Context, claims auth.Claims, input createPaymentRequest, result money.AllocationResult) {
	metadata, err := json.Marshal(map[string]any{
		"unit_id":            input.UnitID,
		"amount":             input.Amount,
		"payment_method":     input.PaymentMethod,
		"reference_number":   input.ReferenceNumber,
		"notes":              input.Notes,
		"unallocated_amount": result.UnallocatedAmount,
	})
	if err != nil {
		s.logger.Error("marshal manual payment audit metadata", "error", err)
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin manual payment audit transaction", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	if _, err := db.New(tx).CreateAuditLogEntry(ctx, db.CreateAuditLogEntryParams{
		OrganizationID: pgtype.UUID{Bytes: claims.OrganizationID, Valid: true},
		ActorID:        pgtype.UUID{Bytes: claims.UserID, Valid: true},
		Action:         "manual_payment.recorded",
		EntityType:     "unit",
		EntityID:       pgtype.UUID{Bytes: input.UnitID, Valid: true},
		Metadata:       metadata,
	}); err != nil {
		s.logger.Error("insert manual payment audit entry", "error", err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit manual payment audit transaction", "error", err)
	}
}

// smsManualPaymentReceipt sends the tenant one receipt SMS summarizing the
// whole allocation (phase 4.1) — not one per invoice leg, so a single manual
// entry that settles arrears + current rent + water doesn't spam three
// messages.
func (s *Server) smsManualPaymentReceipt(ctx context.Context, orgID, unitID uuid.UUID, referenceNumber string, result money.AllocationResult) {
	if s.notify == nil || len(result.Allocations) == 0 {
		return
	}

	var tenantPhone string
	if err := s.pool.QueryRow(ctx, `
		SELECT t.phone
		FROM units u
		JOIN leases l ON l.unit_id = u.id AND l.status = 'active'
		JOIN tenants t ON t.id = l.tenant_id
		WHERE u.id = $1
		LIMIT 1`,
		unitID,
	).Scan(&tenantPhone); err != nil {
		s.logger.Error("lookup tenant phone for manual payment receipt", "error", err)
		return
	}

	applied := int64(0)
	parts := make([]string, 0, len(result.Allocations))
	for _, leg := range result.Allocations {
		applied += leg.Applied
		parts = append(parts, fmt.Sprintf("KES %s -> %s", kesLabel(leg.Applied), purposeLabel(leg.InvoiceType, leg.PeriodMonth)))
	}

	if err := s.notify.SendTemplate(ctx, orgID, tenantPhone, "tenant_rent_received.tmpl", map[string]any{
		"Amount":    kesLabel(applied),
		"Reference": referenceNumber,
		"Breakdown": strings.Join(parts, "; "),
	}); err != nil {
		s.logger.Error("send manual payment receipt sms", "error", err)
	}
}

func kesLabel(cents int64) string {
	return kesmoney.FromCents(cents).String()
}

func purposeLabel(invoiceType string, periodMonth time.Time) string {
	switch invoiceType {
	case "rent":
		return "rent (" + periodMonth.Format("Jan") + ")"
	case "water_garbage":
		return "water & garbage"
	default:
		return invoiceType
	}
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

	if err := idempotency.Validate(input.IdempotencyKey); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		writeJSONError(w, http.StatusBadRequest, "reason is required")
		return
	}

	claims, _ := claimsFromContext(r.Context())

	transfer, err := s.money.ReverseTransferTx(r.Context(), money.ReverseTxInput{
		OrganizationID: claims.OrganizationID,
		TransferID:     transferID,
		Reason:         input.Reason,
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
