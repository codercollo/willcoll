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

// listAgentGrants handles GET /v1/agent-grants?agent_id=... — the current
// (non-revoked) grant per property for one Agent, so the PBAC matrix editor
// can render existing checkbox state instead of always starting blank.
func (s *Server) listAgentGrants(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	agentID, err := uuid.Parse(r.URL.Query().Get("agent_id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT g.id, g.agent_id, g.property_id, g.can_record_payments, g.can_edit_leases,
		       g.can_edit_unit_pricing, g.can_void_payments, g.can_view_financial_reports,
		       g.can_manage_meter_readings, g.granted_by, g.granted_at
		FROM agent_property_grants g
		JOIN users u ON u.id = g.agent_id
		WHERE g.agent_id = $1 AND g.revoked_at IS NULL AND u.organization_id = $2
		ORDER BY g.granted_at`,
		agentID, claims.OrganizationID,
	)
	if err != nil {
		s.logger.Error("list agent grants", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer rows.Close()

	grants := make([]agentGrantResponse, 0)
	for rows.Next() {
		var g agentGrantResponse
		if err := rows.Scan(
			&g.ID, &g.AgentID, &g.PropertyID, &g.CanRecordPayments, &g.CanEditLeases,
			&g.CanEditUnitPricing, &g.CanVoidPayments, &g.CanViewFinancialReports,
			&g.CanManageMeterReadings, &g.GrantedBy, &g.GrantedAt,
		); err != nil {
			s.logger.Error("scan agent grant", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate agent grants", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, grants, "data")
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
