package api

import (
	"net/http"
	"time"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
)

type createRemittanceRequest struct {
	PropertyID     uuid.UUID `json:"property_id"`
	LandlordID     uuid.UUID `json:"landlord_id"`
	Amount         int64     `json:"amount"` // KES cents
	Method         string    `json:"method"`
	Reference      string    `json:"reference"`
	Narrative      string    `json:"narrative"`
	IdempotencyKey string    `json:"idempotency_key"`
}

// createRemittance handles POST /v1/remittances.
func (s *Server) createRemittance(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input createRemittanceRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, _ := claimsFromContext(r.Context())

	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	var landlordPhone string
	if err := tx.QueryRow(r.Context(), `
		SELECT phone
		FROM users
		WHERE id = $1 AND role = 'landlord'`,
		input.LandlordID,
	).Scan(&landlordPhone); err != nil {
		writeJSONError(w, http.StatusNotFound, "landlord not found")
		return
	}

	transfer, err := s.money.ExecuteRemittanceTx(r.Context(), money.RemittanceTxInput{
		OrganizationID: claims.OrganizationID,
		PropertyID:     input.PropertyID,
		LandlordID:     input.LandlordID,
		Amount:         input.Amount,
		Method:         input.Method,
		Reference:      input.Reference,
		Narrative:      input.Narrative,
		IdempotencyKey: input.IdempotencyKey,
		RecordedBy:     claims.UserID,
	})
	if err != nil {
		s.logger.Error("remittance", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if s.notify != nil {
		if err := s.notify.SendTemplate(r.Context(), claims.OrganizationID, landlordPhone, "landlord_remittance_confirmed.tmpl", map[string]any{
			"Amount": input.Amount,
			"Period": time.Now().Format("2006-01"),
		}); err != nil {
			s.logger.Error("send remittance sms", "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, transfer, "data")
}
