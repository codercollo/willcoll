package scoring

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Score is one property_scores row (append-only, spec §3.5/§13.2).
type Score struct {
	ID              uuid.UUID
	PropertyID      uuid.UUID
	ComputedAt      time.Time
	AsOfDate        time.Time
	ModelVersion    string
	NOI             decimal.Decimal
	DSCR            decimal.Decimal
	ScoreValue      decimal.Decimal
	ScoreBand       string
	FeatureSnapshot json.RawMessage
	RequestedBy     uuid.UUID
}

// LatestScore returns the most recently computed score for propertyID.
func (s *Service) LatestScore(ctx context.Context, propertyID uuid.UUID) (Score, error) {
	var sc Score
	err := s.pool.QueryRow(ctx, `
		SELECT id, property_id, computed_at, as_of_date, model_version, noi, dscr,
		       score_value, score_band, feature_snapshot, requested_by
		FROM property_scores
		WHERE property_id = $1
		ORDER BY computed_at DESC
		LIMIT 1`,
		propertyID,
	).Scan(
		&sc.ID, &sc.PropertyID, &sc.ComputedAt, &sc.AsOfDate, &sc.ModelVersion, &sc.NOI, &sc.DSCR,
		&sc.ScoreValue, &sc.ScoreBand, &sc.FeatureSnapshot, &sc.RequestedBy,
	)
	if err != nil {
		return Score{}, err
	}
	return sc, nil
}

// ScoreHistory returns every score computed for propertyID, newest first.
func (s *Service) ScoreHistory(ctx context.Context, propertyID uuid.UUID) ([]Score, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, computed_at, as_of_date, model_version, noi, dscr,
		       score_value, score_band, feature_snapshot, requested_by
		FROM property_scores
		WHERE property_id = $1
		ORDER BY computed_at DESC`,
		propertyID,
	)
	if err != nil {
		return nil, fmt.Errorf("list score history: %w", err)
	}
	defer rows.Close()

	out := make([]Score, 0)
	for rows.Next() {
		var sc Score
		if err := rows.Scan(
			&sc.ID, &sc.PropertyID, &sc.ComputedAt, &sc.AsOfDate, &sc.ModelVersion, &sc.NOI, &sc.DSCR,
			&sc.ScoreValue, &sc.ScoreBand, &sc.FeatureSnapshot, &sc.RequestedBy,
		); err != nil {
			return nil, fmt.Errorf("scan score: %w", err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate score history: %w", err)
	}
	return out, nil
}
