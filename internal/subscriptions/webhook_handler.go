package subscriptions

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

type intasendWebhookPayload struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Invoice struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"invoice"`
}

// IntasendWebhook handles POST /v1/webhooks/intasend. It verifies the gateway
// signature before touching the database, then idempotently marks the matching
// subscription charge COMPLETE/FAILED.
func (s *Service) IntasendWebhook(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if !s.validSignature(body, r.Header.Get("X-Intasend-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload intasendWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	reference := payload.ID
	if reference == "" {
		reference = payload.Invoice.ID
	}
	state := payload.State
	if state == "" {
		state = payload.Invoice.State
	}

	if reference == "" {
		http.Error(w, "missing gateway reference", http.StatusBadRequest)
		return
	}

	newStatus := ""
	switch state {
	case "COMPLETE":
		newStatus = "COMPLETE"
	case "FAILED":
		newStatus = "FAILED"
	default:
		// Unknown intermediate states are logged by the API's request logger;
		// no state change is safe to apply.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_, err = s.pool.Exec(r.Context(), `
		INSERT INTO subscription_charges (
			organization_id, manager_id, tier, amount_kes, gateway_provider,
			gateway_reference, status, billing_period_start, billing_period_end, raw_payload
		)
		SELECT organization_id, manager_id, tier, amount_kes, 'intasend',
		       $1, $2, billing_period_start, billing_period_end, $3
		FROM subscription_charges
		WHERE gateway_provider = 'intasend' AND gateway_reference = $1
		ON CONFLICT (gateway_provider, gateway_reference) DO UPDATE SET
			status = CASE
				WHEN subscription_charges.status = 'PENDING' THEN EXCLUDED.status
				ELSE subscription_charges.status
			END,
			raw_payload = EXCLUDED.raw_payload,
			updated_at = now()`,
		reference, newStatus, json.RawMessage(body),
	)
	if err != nil {
		http.Error(w, "store webhook", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) validSignature(body []byte, signature string) bool {
	if s.webhookSecret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
