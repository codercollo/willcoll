package scoring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrSidecarUnavailable wraps any failure reaching or parsing the sidecar's
// response, so callers can distinguish "sidecar down" from "not entitled"
// (ErrAddonNotActive) or a genuine validation error.
var ErrSidecarUnavailable = errors.New("scoring sidecar request failed")

type sidecarScoreRequest struct {
	RequestedBy       uuid.UUID       `json:"requested_by"`
	OperatingExpenses decimal.Decimal `json:"operating_expenses"`
	AnnualDebtService decimal.Decimal `json:"annual_debt_service"`
	OtherIncome       decimal.Decimal `json:"other_income"`
}

// sidecarScoreResponse is the already-persisted property_scores row the
// sidecar returns — it owns the INSERT (spec §13.3.1), so this Go client
// never writes property_scores itself, only relays what the sidecar did.
type sidecarScoreResponse struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	PropertyID     uuid.UUID       `json:"property_id"`
	ComputedAt     time.Time       `json:"computed_at"`
	AsOfDate       string          `json:"as_of_date"`
	ModelVersion   string          `json:"model_version"`
	NOI            decimal.Decimal `json:"noi"`
	DSCR           decimal.Decimal `json:"dscr"`
	ScoreValue     decimal.Decimal `json:"score_value"`
	ScoreBand      string          `json:"score_band"`
	RequestedBy    uuid.UUID       `json:"requested_by"`
}

// RequestScoreInput carries the underwriting figures Willcoll's ledger has
// no source for (no expense/loan tables) — the caller supplies them rather
// than the sidecar guessing (spec: "no guessing").
type RequestScoreInput struct {
	PropertyID        uuid.UUID
	RequestedBy       uuid.UUID
	OperatingExpenses decimal.Decimal
	AnnualDebtService decimal.Decimal
	OtherIncome       decimal.Decimal
}

// RequestScore gates on the add-on (§12.3), POSTs the sidecar's
// /v1/score/{property_id} with the shared-secret header (§13.3), and
// returns the row the sidecar already persisted. Single-property only —
// there is no batch/organization-wide variant.
func (s *Service) RequestScore(ctx context.Context, organizationID uuid.UUID, input RequestScoreInput) (Score, error) {
	active, err := s.IsAddonActive(ctx, organizationID)
	if err != nil {
		return Score{}, err
	}
	if !active {
		return Score{}, ErrAddonNotActive
	}

	body, err := json.Marshal(sidecarScoreRequest{
		RequestedBy:       input.RequestedBy,
		OperatingExpenses: input.OperatingExpenses,
		AnnualDebtService: input.AnnualDebtService,
		OtherIncome:       input.OtherIncome,
	})
	if err != nil {
		return Score{}, fmt.Errorf("build sidecar request body: %w", err)
	}

	url := fmt.Sprintf("%s/v1/score/%s", s.sidecarBaseURL, input.PropertyID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Score{}, fmt.Errorf("build sidecar request: %w", err)
	}
	req.Header.Set("X-Sidecar-Shared-Secret", s.sharedSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return Score{}, fmt.Errorf("%w: %v", ErrSidecarUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return Score{}, fmt.Errorf("%w: status %d: %s", ErrSidecarUnavailable, resp.StatusCode, respBody)
	}

	var out sidecarScoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Score{}, fmt.Errorf("%w: decode response: %v", ErrSidecarUnavailable, err)
	}

	asOf, err := time.Parse("2006-01-02", out.AsOfDate)
	if err != nil {
		return Score{}, fmt.Errorf("%w: parse as_of_date: %v", ErrSidecarUnavailable, err)
	}

	return Score{
		ID:           out.ID,
		PropertyID:   out.PropertyID,
		ComputedAt:   out.ComputedAt,
		AsOfDate:     asOf,
		ModelVersion: out.ModelVersion,
		NOI:          out.NOI,
		DSCR:         out.DSCR,
		ScoreValue:   out.ScoreValue,
		ScoreBand:    out.ScoreBand,
		RequestedBy:  out.RequestedBy,
	}, nil
}
