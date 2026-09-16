package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

const (
	permissionRecordPayments       = "can_record_payments"
	permissionEditLeases           = "can_edit_leases"
	permissionEditUnitPricing      = "can_edit_unit_pricing"
	permissionVoidPayments         = "can_void_payments"
	permissionViewFinancialReports = "can_view_financial_reports"
	permissionManageMeterReadings  = "can_manage_meter_readings"
)

// requireRole gates a handler behind one of the allowed RBAC roles (spec §2.1).
func (s *Server) requireRole(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := claimsFromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
				return
			}

			if !roleAllowed(claims.Role, allowed) {
				writeJSONError(w, http.StatusForbidden, "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requirePermission adds the PBAC layer on top of the RBAC role check. Roles
// other than Agent pass on role alone; Agents must also have an active grant
// row for the resolved property with the specific permission bit set.
func (s *Server) requirePermission(permission string, allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := claimsFromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
				return
			}

			if !roleAllowed(claims.Role, allowed) {
				writeJSONError(w, http.StatusForbidden, "insufficient permissions")
				return
			}

			if claims.Role != "agent" {
				next.ServeHTTP(w, r)
				return
			}

			tx, ok := requestTxFromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
				return
			}

			propertyID, ok := s.resolvePropertyID(r.Context(), tx, r)
			if !ok {
				writeJSONError(w, http.StatusForbidden, "not granted for this property")
				return
			}

			granted, err := s.agentGranted(r.Context(), tx, claims.UserID, propertyID, permission)
			if err != nil {
				s.logger.Error("check agent grant", "error", err)
				writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
				return
			}
			if !granted {
				writeJSONError(w, http.StatusForbidden, "not granted for this property")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func roleAllowed(role string, allowed []string) bool {
	for _, a := range allowed {
		if role == a {
			return true
		}
	}
	return false
}

func (s *Server) resolvePropertyID(ctx context.Context, tx pgx.Tx, r *http.Request) (uuid.UUID, bool) {
	params := httprouter.ParamsFromContext(r.Context())

	if raw := params.ByName("property_id"); raw != "" {
		id, err := uuid.Parse(raw)
		return id, err == nil
	}

	if raw := params.ByName("unit_id"); raw != "" {
		unitID, err := uuid.Parse(raw)
		if err != nil {
			return uuid.Nil, false
		}
		var propertyID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT property_id
			FROM units
			WHERE id = $1`,
			unitID,
		).Scan(&propertyID); err != nil {
			return uuid.Nil, false
		}
		return propertyID, true
	}

	// Existing property/unit routes use :id. Try property first, then unit.
	if raw := params.ByName("id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return uuid.Nil, false
		}

		var propertyID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM properties WHERE id = $1`, id).Scan(&propertyID); err == nil {
			return propertyID, true
		}
		if err := tx.QueryRow(ctx, `SELECT property_id FROM units WHERE id = $1`, id).Scan(&propertyID); err == nil {
			return propertyID, true
		}
		var unitID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT unit_id FROM leases WHERE id = $1`, id).Scan(&unitID); err == nil {
			if err := tx.QueryRow(ctx, `SELECT property_id FROM units WHERE id = $1`, unitID).Scan(&propertyID); err == nil {
				return propertyID, true
			}
		}
		if err := tx.QueryRow(ctx, `SELECT unit_id FROM meters WHERE id = $1`, id).Scan(&unitID); err == nil {
			if err := tx.QueryRow(ctx, `SELECT property_id FROM units WHERE id = $1`, unitID).Scan(&propertyID); err == nil {
				return propertyID, true
			}
		}
		var invoiceID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT invoice_id FROM ledger_transfers WHERE id = $1`, id).Scan(&invoiceID); err == nil && invoiceID != nil {
			if err := tx.QueryRow(ctx, `
				SELECT u.property_id
				FROM invoices i
				JOIN leases l ON l.id = i.lease_id
				JOIN units u ON u.id = l.unit_id
				WHERE i.id = $1`, *invoiceID,
			).Scan(&propertyID); err == nil {
				return propertyID, true
			}
		}
		return uuid.Nil, false
	}

	return uuid.Nil, false
}

func (s *Server) agentGranted(ctx context.Context, tx pgx.Tx, agentID, propertyID uuid.UUID, permission string) (bool, error) {
	column, ok := permissionColumn(permission)
	if !ok {
		return false, fmt.Errorf("unknown permission %q", permission)
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM agent_property_grants
		WHERE agent_id = $1
		  AND property_id = $2
		  AND revoked_at IS NULL
		ORDER BY granted_at DESC
		LIMIT 1`,
		column,
	)

	var granted bool
	err := tx.QueryRow(ctx, query, agentID, propertyID).Scan(&granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return granted, nil
}

func permissionColumn(permission string) (string, bool) {
	switch permission {
	case permissionRecordPayments:
		return "can_record_payments", true
	case permissionEditLeases:
		return "can_edit_leases", true
	case permissionEditUnitPricing:
		return "can_edit_unit_pricing", true
	case permissionVoidPayments:
		return "can_void_payments", true
	case permissionViewFinancialReports:
		return "can_view_financial_reports", true
	case permissionManageMeterReadings:
		return "can_manage_meter_readings", true
	default:
		return "", false
	}
}
