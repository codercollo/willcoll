package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// initiateSubscriptionCharge handles POST /v1/organization/subscription/charge.
func (s *Server) initiateSubscriptionCharge(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())

	result, err := s.subscriptions.InitiateSubscriptionChargeTx(r.Context(), claims.UserID)
	if err != nil {
		s.logger.Error("initiate subscription charge", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"charge_id":    result.ChargeID,
		"checkout_url": result.CheckoutURL,
		"tier":         result.Tier,
		"fee_kes":      result.FeeKES,
	}, "data")
}

// getSubscription handles GET /v1/organization/subscription.
func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())

	status, err := s.subscriptions.GetSubscriptionStatus(r.Context(), claims.UserID)
	if err != nil {
		s.logger.Error("get subscription", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tier":                  status.Tier,
		"fee_kes":               status.FeeKES,
		"last_charge_status":    status.LastChargeStatus,
		"last_charge_reference": status.LastChargeReference,
	}, "data")
}
