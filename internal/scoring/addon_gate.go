package scoring

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAddonNotActive is returned by RequestScore when the Organization hasn't
// activated the Verified Property Score add-on (spec §12.3).
var ErrAddonNotActive = errors.New("verified property score add-on is not active for this organization")

// IsAddonActive reports whether organizationID has an active
// verified_property_score row in organization_addons.
func (s *Service) IsAddonActive(ctx context.Context, organizationID uuid.UUID) (bool, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT status::text
		FROM organization_addons
		WHERE organization_id = $1 AND addon_key = 'verified_property_score'`,
		organizationID,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check addon status: %w", err)
	}
	return status == "active", nil
}

// ActivateAddon creates or reactivates the Organization's verified_property_score
// add-on at monthlyFeeKES (spec §12.3 — a flat, negotiated per-Organization fee).
func (s *Service) ActivateAddon(ctx context.Context, organizationID uuid.UUID, monthlyFeeKES float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO organization_addons (organization_id, addon_key, status, monthly_fee_kes, activated_at, cancelled_at)
		VALUES ($1, 'verified_property_score', 'active', $2, now(), NULL)
		ON CONFLICT (organization_id, addon_key) DO UPDATE SET
			status = 'active', monthly_fee_kes = $2, activated_at = now(), cancelled_at = NULL`,
		organizationID, monthlyFeeKES,
	)
	if err != nil {
		return fmt.Errorf("activate addon: %w", err)
	}
	return nil
}

// CancelAddon deactivates the Organization's verified_property_score add-on.
func (s *Service) CancelAddon(ctx context.Context, organizationID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE organization_addons
		SET status = 'cancelled', cancelled_at = now()
		WHERE organization_id = $1 AND addon_key = 'verified_property_score'`,
		organizationID,
	)
	if err != nil {
		return fmt.Errorf("cancel addon: %w", err)
	}
	return nil
}
