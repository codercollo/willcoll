package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll-sys/internal/notify"
	"github.com/julienschmidt/httprouter"
)

type smsTemplateResponse struct {
	Key      string   `json:"key"`
	Actor    string   `json:"actor"`
	Vars     []string `json:"vars"`
	Body     string   `json:"body"`
	Override bool     `json:"override"`
}

type updateSMSTemplateRequest struct {
	Body string `json:"body"`
}

// listSMSTemplates handles GET /v1/organization/sms-templates — the six
// fixed Tenant/Landlord templates internal/notify actually renders (spec
// Phase 8.2), each with the current effective body (an Organization's saved
// override, or the shipped default) and the variable names that template is
// allowed to reference.
func (s *Server) listSMSTemplates(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT template_key, body
		FROM sms_template_overrides
		WHERE organization_id = $1`,
		claims.OrganizationID,
	)
	if err != nil {
		s.logger.Error("list sms template overrides", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	overrides := make(map[string]string)
	for rows.Next() {
		var key, body string
		if err := rows.Scan(&key, &body); err != nil {
			rows.Close()
			s.logger.Error("scan sms template override", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		overrides[key] = body
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate sms template overrides", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	out := make([]smsTemplateResponse, 0, len(notify.Templates))
	for _, def := range notify.Templates {
		body, hasOverride := overrides[def.Key]
		if !hasOverride {
			defaultBody, err := notify.DefaultBody(def.Key)
			if err != nil {
				s.logger.Error("load default sms template", "error", err, "template", def.Key)
				writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
				return
			}
			body = defaultBody
		}
		out = append(out, smsTemplateResponse{
			Key:      def.Key,
			Actor:    string(def.Actor),
			Vars:     def.Vars,
			Body:     body,
			Override: hasOverride,
		})
	}

	writeJSON(w, http.StatusOK, out, "data")
}

// updateSMSTemplate handles PATCH /v1/organization/sms-templates/:key —
// validated against that template's own variable set (never a free-text box
// accepting a placeholder the backend won't fill) before it's saved.
func (s *Server) updateSMSTemplate(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	key := ps.ByName("key")

	var input updateSMSTemplateRequest
	if !readJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Body) == "" {
		writeJSONError(w, http.StatusBadRequest, "body is required")
		return
	}

	if err := notify.ValidateTemplateBody(key, input.Body); err != nil {
		if errors.Is(err, notify.ErrUnknownTemplate) {
			writeJSONError(w, http.StatusNotFound, "unknown template")
			return
		}
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO sms_template_overrides (organization_id, template_key, body, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id, template_key) DO UPDATE SET
			body = EXCLUDED.body,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()`,
		claims.OrganizationID, key, input.Body, claims.UserID,
	); err != nil {
		s.logger.Error("save sms template override", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, smsTemplateResponse{Key: key, Body: input.Body, Override: true}, "data")
}
