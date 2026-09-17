package api

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll-sys/internal/domain"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/pkg/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

type createLeaseRequest struct {
	FullName         string   `json:"full_name"`
	Phone            string   `json:"phone"`
	IDNumber         string   `json:"id_number"`
	MonthlyRent      float64  `json:"monthly_rent"`
	DepositPaid      float64  `json:"deposit_paid"`
	StartDate        string   `json:"start_date"`
	RentDueDay       int      `json:"rent_due_day"`
	LateFeePctPerDay *float64 `json:"late_fee_pct_per_day"`
}

type terminateLeaseRequest struct {
	Reason string `json:"reason"`

	// RefundAmount is the Manager's deposit refund/forfeit decision (spec
	// §3.2): KES cents refunded to the tenant now; deposit_paid minus this
	// stays in deposit_holding, forfeited. Zero/omitted = full forfeiture,
	// no refund transfer posted.
	RefundAmount    int64  `json:"refund_amount"`
	RefundMethod    string `json:"refund_method"`
	RefundReference string `json:"refund_reference"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type leaseResponse struct {
	ID               uuid.UUID  `json:"id"`
	UnitID           uuid.UUID  `json:"unit_id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	MonthlyRent      float64    `json:"monthly_rent"`
	DepositPaid      float64    `json:"deposit_paid"`
	StartDate        time.Time  `json:"start_date"`
	EndDate          *time.Time `json:"end_date"`
	RentDueDay       int        `json:"rent_due_day"`
	LateFeePctPerDay float64    `json:"late_fee_pct_per_day"`
	Status           string     `json:"status"`
	TerminatedReason *string    `json:"terminated_reason"`
	TerminatedAt     *time.Time `json:"terminated_at"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
}

// createLease handles POST /v1/units/:id/leases.
func (s *Server) createLease(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	unitID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid unit id")
		return
	}

	var input createLeaseRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var unitStatus string
	var propertyID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		SELECT u.status::text, u.property_id
		FROM units u
		WHERE u.id = $1`,
		unitID,
	).Scan(&unitStatus, &propertyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "unit not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup unit", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if unitStatus != "vacant" {
		writeJSONError(w, http.StatusConflict, "unit is not vacant")
		return
	}

	var (
		propertyRentDueDay int
		propertyLateFee    float64
	)
	if err := tx.QueryRow(r.Context(), `
		SELECT rent_due_day, late_fee_pct_per_day
		FROM properties
		WHERE id = $1`,
		propertyID,
	).Scan(&propertyRentDueDay, &propertyLateFee); err != nil {
		s.logger.Error("lookup property defaults", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	startDate, err := time.Parse("2006-01-02", strings.TrimSpace(input.StartDate))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "start_date must be YYYY-MM-DD")
		return
	}

	rentDueDay := input.RentDueDay
	if rentDueDay == 0 {
		rentDueDay = propertyRentDueDay
	}
	lateFee := propertyLateFee
	if input.LateFeePctPerDay != nil {
		lateFee = *input.LateFeePctPerDay
	}

	tenant, err := domain.NewTenant(domain.NewTenantInput{
		OrganizationID: claims.OrganizationID,
		FullName:       input.FullName,
		Phone:          input.Phone,
		IDNumber:       input.IDNumber,
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var tenantID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		INSERT INTO tenants (organization_id, full_name, phone, id_number)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id`,
		tenant.OrganizationID(), tenant.FullName(), tenant.Phone(), tenant.IDNumber(),
	).Scan(&tenantID)
	if err != nil {
		s.logger.Error("insert tenant", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	lease, err := domain.NewLease(domain.NewLeaseInput{
		UnitID:           unitID,
		TenantID:         tenantID,
		MonthlyRent:      input.MonthlyRent,
		DepositPaid:      input.DepositPaid,
		StartDate:        startDate,
		RentDueDay:       rentDueDay,
		LateFeePctPerDay: lateFee,
		Status:           "active",
		CreatedBy:        claims.UserID,
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var l leaseResponse
	err = tx.QueryRow(r.Context(), `
		INSERT INTO leases (
			unit_id, tenant_id, monthly_rent, deposit_paid, start_date,
			rent_due_day, late_fee_pct_per_day, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8)
		RETURNING id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date,
		          end_date, rent_due_day, late_fee_pct_per_day, status::text,
		          terminated_reason, terminated_at, created_by, created_at`,
		lease.UnitID(), lease.TenantID(), lease.MonthlyRent(), lease.DepositPaid(),
		lease.StartDate(), lease.RentDueDay(), lease.LateFeePctPerDay(), lease.CreatedBy(),
	).Scan(
		&l.ID, &l.UnitID, &l.TenantID, &l.MonthlyRent, &l.DepositPaid, &l.StartDate,
		&l.EndDate, &l.RentDueDay, &l.LateFeePctPerDay, &l.Status,
		&l.TerminatedReason, &l.TerminatedAt, &l.CreatedBy, &l.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSONError(w, http.StatusConflict, "unit already has an active lease")
			return
		}
		s.logger.Error("insert lease", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE units
		SET status = 'occupied'
		WHERE id = $1`,
		unitID,
	); err != nil {
		s.logger.Error("occupy unit", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if s.notify != nil {
		if err := s.notify.SendTemplate(r.Context(), claims.OrganizationID, tenant.Phone(), "tenant_welcome.tmpl", map[string]any{
			"RentDueDay": rentDueDay,
		}); err != nil {
			s.logger.Error("send welcome sms", "error", err)
		}
	}

	depositCents := int64(math.Round(input.DepositPaid * 100))
	if _, err := s.money.ExecuteDepositTx(r.Context(), money.DepositTxInput{
		OrganizationID: claims.OrganizationID,
		TenantID:       tenantID,
		UnitID:         unitID,
		PropertyID:     propertyID,
		Amount:         depositCents,
		Method:         "cash",
		IdempotencyKey: "deposit:" + l.ID.String(),
		RecordedBy:     claims.UserID,
	}); err != nil {
		s.logger.Error("post deposit", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, l, "data")
}

// terminateLease handles POST /v1/leases/:id/terminate.
func (s *Server) terminateLease(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	leaseID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid lease id")
		return
	}

	var input terminateLeaseRequest
	if !readJSON(w, r, &input) {
		return
	}
	if input.RefundAmount > 0 {
		if err := idempotency.Validate(input.IdempotencyKey); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var unitID, tenantID, propertyID uuid.UUID
	var status string
	err = tx.QueryRow(r.Context(), `
		SELECT l.unit_id, l.tenant_id, u.property_id, l.status::text
		FROM leases l
		JOIN units u ON u.id = l.unit_id
		WHERE l.id = $1`,
		leaseID,
	).Scan(&unitID, &tenantID, &propertyID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "lease not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup lease", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if status != "active" {
		writeJSONError(w, http.StatusConflict, "lease is not active")
		return
	}

	var l leaseResponse
	err = tx.QueryRow(r.Context(), `
		UPDATE leases
		SET status = 'terminated',
		    terminated_reason = NULLIF($2, ''),
		    terminated_at = now(),
		    end_date = COALESCE(end_date, CURRENT_DATE)
		WHERE id = $1
		RETURNING id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date,
		          end_date, rent_due_day, late_fee_pct_per_day, status::text,
		          terminated_reason, terminated_at, created_by, created_at`,
		leaseID, strings.TrimSpace(input.Reason),
	).Scan(
		&l.ID, &l.UnitID, &l.TenantID, &l.MonthlyRent, &l.DepositPaid, &l.StartDate,
		&l.EndDate, &l.RentDueDay, &l.LateFeePctPerDay, &l.Status,
		&l.TerminatedReason, &l.TerminatedAt, &l.CreatedBy, &l.CreatedAt,
	)
	if err != nil {
		s.logger.Error("terminate lease", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE units
		SET status = 'vacant'
		WHERE id = $1`,
		unitID,
	); err != nil {
		s.logger.Error("vacate unit", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if input.RefundAmount > 0 {
		if _, err := s.money.ExecuteDepositRefundTx(r.Context(), money.DepositRefundTxInput{
			OrganizationID: claims.OrganizationID,
			TenantID:       tenantID,
			UnitID:         unitID,
			PropertyID:     propertyID,
			Amount:         input.RefundAmount,
			Method:         input.RefundMethod,
			Reference:      input.RefundReference,
			Narrative:      "Deposit refund on move-out",
			IdempotencyKey: input.IdempotencyKey,
			RecordedBy:     claims.UserID,
		}); err != nil {
			s.logger.Error("refund deposit", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
	}

	writeJSON(w, http.StatusOK, l, "data")
}
