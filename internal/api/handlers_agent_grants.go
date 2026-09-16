package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

type setAgentGrantRequest struct {
	AgentID                 uuid.UUID `json:"agent_id"`
	PropertyID              uuid.UUID `json:"property_id"`
	CanRecordPayments       bool      `json:"can_record_payments"`
	CanEditLeases           bool      `json:"can_edit_leases"`
	CanEditUnitPricing      bool      `json:"can_edit_unit_pricing"`
	CanVoidPayments         bool      `json:"can_void_payments"`
	CanViewFinancialReports bool      `json:"can_view_financial_reports"`
	CanManageMeterReadings  bool      `json:"can_manage_meter_readings"`
}

type agentGrantResponse struct {
	ID                      uuid.UUID `json:"id"`
	AgentID                 uuid.UUID `json:"agent_id"`
	PropertyID              uuid.UUID `json:"property_id"`
	CanRecordPayments       bool      `json:"can_record_payments"`
	CanEditLeases           bool      `json:"can_edit_leases"`
	CanEditUnitPricing      bool      `json:"can_edit_unit_pricing"`
	CanVoidPayments         bool      `json:"can_void_payments"`
	CanViewFinancialReports bool      `json:"can_view_financial_reports"`
	CanManageMeterReadings  bool      `json:"can_manage_meter_readings"`
	GrantedBy               uuid.UUID `json:"granted_by"`
	GrantedAt               time.Time `json:"granted_at"`
}

// setAgentGrant handles POST /v1/agent-grants.
func (s *Server) setAgentGrant(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input setAgentGrantRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var agentRole string
	err := tx.QueryRow(r.Context(), `
		SELECT role::text
		FROM users
		WHERE id = $1 AND organization_id = $2`,
		input.AgentID, claims.OrganizationID,
	).Scan(&agentRole)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup agent", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if agentRole != "agent" {
		writeJSONError(w, http.StatusUnprocessableEntity, "grant target must be an agent")
		return
	}

	var propertyExists bool
	if err := tx.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM properties WHERE id = $1)`,
		input.PropertyID,
	).Scan(&propertyExists); err != nil {
		s.logger.Error("lookup property", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if !propertyExists {
		writeJSONError(w, http.StatusNotFound, "property not found")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_property_grants
		SET revoked_at = now()
		WHERE agent_id = $1 AND property_id = $2 AND revoked_at IS NULL`,
		input.AgentID, input.PropertyID,
	); err != nil {
		s.logger.Error("revoke prior grants", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var grant agentGrantResponse
	err = tx.QueryRow(r.Context(), `
		INSERT INTO agent_property_grants (
			agent_id, property_id, can_record_payments, can_edit_leases,
			can_edit_unit_pricing, can_void_payments, can_view_financial_reports,
			can_manage_meter_readings, granted_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, agent_id, property_id, can_record_payments, can_edit_leases,
		          can_edit_unit_pricing, can_void_payments, can_view_financial_reports,
		          can_manage_meter_readings, granted_by, granted_at`,
		input.AgentID, input.PropertyID, input.CanRecordPayments, input.CanEditLeases,
		input.CanEditUnitPricing, input.CanVoidPayments, input.CanViewFinancialReports,
		input.CanManageMeterReadings, claims.UserID,
	).Scan(
		&grant.ID, &grant.AgentID, &grant.PropertyID, &grant.CanRecordPayments,
		&grant.CanEditLeases, &grant.CanEditUnitPricing, &grant.CanVoidPayments,
		&grant.CanViewFinancialReports, &grant.CanManageMeterReadings,
		&grant.GrantedBy, &grant.GrantedAt,
	)
	if err != nil {
		s.logger.Error("insert agent grant", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, grant, "data")
}
