package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
)

type auditLogResponse struct {
	ID         uuid.UUID  `json:"id"`
	PropertyID *uuid.UUID `json:"property_id"`
	ActorID    *uuid.UUID `json:"actor_id"`
	Action     string     `json:"action"`
	EntityType string     `json:"entity_type"`
	EntityID   *uuid.UUID `json:"entity_id"`
	CreatedAt  time.Time  `json:"created_at"`
}

// listAuditLog handles GET /v1/audit-log.
func (s *Server) listAuditLog(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	propertyID := uuid.Nil
	if raw := r.URL.Query().Get("property_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid property_id")
			return
		}
		propertyID = id
	}

	query := `
		SELECT id, property_id, actor_id, action, entity_type, entity_id, created_at
		FROM audit_log`
	args := []any{}
	if propertyID != uuid.Nil {
		query += ` WHERE property_id = $1`
		args = append(args, propertyID)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := tx.Query(r.Context(), query, args...)
	if err != nil {
		s.logger.Error("audit log", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer rows.Close()

	out := make([]auditLogResponse, 0)
	for rows.Next() {
		var a auditLogResponse
		if err := rows.Scan(&a.ID, &a.PropertyID, &a.ActorID, &a.Action, &a.EntityType, &a.EntityID, &a.CreatedAt); err != nil {
			s.logger.Error("scan audit log", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate audit log", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, out, "data")
}
