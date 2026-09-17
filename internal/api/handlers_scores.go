package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/scoring"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/julienschmidt/httprouter"
	"github.com/shopspring/decimal"
)

// auditScoreAction writes one append-only audit_log entry (phase 24.1).
// Best-effort: a logging failure never fails the request it's describing.
func (s *Server) auditScoreAction(ctx context.Context, claims auth.Claims, action string, propertyID *uuid.UUID, metadata map[string]any) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		s.logger.Error("marshal audit metadata", "error", err)
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin audit transaction", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	entityID := pgtype.UUID{}
	if propertyID != nil {
		entityID = pgtype.UUID{Bytes: *propertyID, Valid: true}
	}

	if _, err := db.New(tx).CreateAuditLogEntry(ctx, db.CreateAuditLogEntryParams{
		OrganizationID: pgtype.UUID{Bytes: claims.OrganizationID, Valid: true},
		PropertyID:     entityID,
		ActorID:        pgtype.UUID{Bytes: claims.UserID, Valid: true},
		Action:         action,
		EntityType:     "property_score",
		EntityID:       entityID,
		Metadata:       raw,
	}); err != nil {
		s.logger.Error("insert audit entry", "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit audit transaction", "error", err)
	}
}

type scoreResponse struct {
	ID           uuid.UUID `json:"id"`
	PropertyID   uuid.UUID `json:"property_id"`
	ComputedAt   string    `json:"computed_at"`
	AsOfDate     string    `json:"as_of_date"`
	ModelVersion string    `json:"model_version"`
	NOI          string    `json:"noi"`
	DSCR         string    `json:"dscr"`
	ScoreValue   string    `json:"score_value"`
	ScoreBand    string    `json:"score_band"`
}

func toScoreResponse(sc scoring.Score) scoreResponse {
	return scoreResponse{
		ID:           sc.ID,
		PropertyID:   sc.PropertyID,
		ComputedAt:   sc.ComputedAt.Format("2006-01-02T15:04:05Z07:00"),
		AsOfDate:     sc.AsOfDate.Format("2006-01-02"),
		ModelVersion: sc.ModelVersion,
		NOI:          sc.NOI.String(),
		DSCR:         sc.DSCR.String(),
		ScoreValue:   sc.ScoreValue.String(),
		ScoreBand:    sc.ScoreBand,
	}
}

// canViewProperty reports whether claims may read propertyID's score: any
// Manager/Agent in the org, or the Landlord who owns it (spec §13.4).
func (s *Server) canViewProperty(r *http.Request, propertyID uuid.UUID) (bool, error) {
	claims, ok := claimsFromContext(r.Context())
	if !ok {
		return false, nil
	}
	if claims.Role != "landlord" {
		return true, nil
	}
	var owns bool
	err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM property_ownership WHERE property_id = $1 AND landlord_id = $2)`,
		propertyID, claims.UserID,
	).Scan(&owns)
	return owns, err
}

type requestScoreRequest struct {
	OperatingExpenses decimal.Decimal `json:"operating_expenses"`
	AnnualDebtService decimal.Decimal `json:"annual_debt_service"`
	OtherIncome       decimal.Decimal `json:"other_income"`
}

// requestScore handles POST /v1/properties/:id/score — Manager-only,
// premium-gated (spec §13.4). operating_expenses/annual_debt_service are
// required: Willcoll's ledger has no expense/loan tables, so NOI/DSCR never
// guess them — the Manager supplies the real underwriting figures.
func (s *Server) requestScore(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	propertyID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid property id")
		return
	}
	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	var input requestScoreRequest
	if !readJSON(w, r, &input) {
		return
	}
	if input.OperatingExpenses.IsZero() || input.AnnualDebtService.IsZero() {
		writeJSONError(w, http.StatusBadRequest, "operating_expenses and annual_debt_service are required")
		return
	}

	sc, err := s.scoring.RequestScore(r.Context(), claims.OrganizationID, scoring.RequestScoreInput{
		PropertyID:        propertyID,
		RequestedBy:       claims.UserID,
		OperatingExpenses: input.OperatingExpenses,
		AnnualDebtService: input.AnnualDebtService,
		OtherIncome:       input.OtherIncome,
	})
	switch {
	case errors.Is(err, scoring.ErrAddonNotActive):
		writeJSONError(w, http.StatusPaymentRequired, "the Verified Property Score add-on is not active for this organization")
		return
	case errors.Is(err, scoring.ErrSidecarUnavailable):
		s.logger.Error("scoring sidecar", "error", err)
		writeJSONError(w, http.StatusBadGateway, "the scoring service is unavailable, please try again shortly")
		return
	case err != nil:
		s.logger.Error("request score", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	s.auditScoreAction(r.Context(), claims, "property_score.requested", &propertyID, map[string]any{
		"score_band": sc.ScoreBand, "score_value": sc.ScoreValue.String(),
	})

	writeJSON(w, http.StatusCreated, toScoreResponse(sc), "data")
}

// getLatestScore handles GET /v1/properties/:id/score.
func (s *Server) getLatestScore(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	propertyID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid property id")
		return
	}
	if ok, err := s.canViewProperty(r, propertyID); err != nil {
		s.logger.Error("check property ownership", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	} else if !ok {
		writeJSONError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	sc, err := s.scoring.LatestScore(r.Context(), propertyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "no score has been computed for this property yet")
		return
	}
	if err != nil {
		s.logger.Error("get latest score", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, toScoreResponse(sc), "data")
}

// getScoreHistory handles GET /v1/properties/:id/score/history.
func (s *Server) getScoreHistory(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	propertyID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid property id")
		return
	}
	if ok, err := s.canViewProperty(r, propertyID); err != nil {
		s.logger.Error("check property ownership", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	} else if !ok {
		writeJSONError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	history, err := s.scoring.ScoreHistory(r.Context(), propertyID)
	if err != nil {
		s.logger.Error("get score history", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	out := make([]scoreResponse, 0, len(history))
	for _, sc := range history {
		out = append(out, toScoreResponse(sc))
	}
	writeJSON(w, http.StatusOK, out, "data")
}

// getScoreAddon handles GET
// /v1/organization/addons/verified-property-score.
func (s *Server) getScoreAddon(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())
	status, err := s.scoring.GetAddonStatus(r.Context(), claims.OrganizationID)
	if err != nil {
		s.logger.Error("get score addon", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          status.Status,
		"monthly_fee_kes": status.MonthlyFeeKES,
	}, "data")
}

type activateScoreAddonRequest struct {
	MonthlyFeeKES float64 `json:"monthly_fee_kes"`
}

// activateScoreAddon handles POST
// /v1/organization/addons/verified-property-score/activate (Manager only).
func (s *Server) activateScoreAddon(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input activateScoreAddonRequest
	if !readJSON(w, r, &input) {
		return
	}
	if input.MonthlyFeeKES <= 0 {
		writeJSONError(w, http.StatusBadRequest, "monthly_fee_kes must be greater than zero")
		return
	}

	claims, _ := claimsFromContext(r.Context())
	if err := s.scoring.ActivateAddon(r.Context(), claims.OrganizationID, input.MonthlyFeeKES); err != nil {
		s.logger.Error("activate score addon", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	s.auditScoreAction(r.Context(), claims, "addon.verified_property_score.activated", nil, map[string]any{"monthly_fee_kes": input.MonthlyFeeKES})

	writeJSON(w, http.StatusOK, map[string]string{"status": "active"}, "data")
}

// cancelScoreAddon handles POST
// /v1/organization/addons/verified-property-score/cancel (Manager only).
func (s *Server) cancelScoreAddon(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())
	if err := s.scoring.CancelAddon(r.Context(), claims.OrganizationID); err != nil {
		s.logger.Error("cancel score addon", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	s.auditScoreAction(r.Context(), claims, "addon.verified_property_score.cancelled", nil, nil)

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"}, "data")
}
