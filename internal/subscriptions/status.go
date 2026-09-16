package subscriptions

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SubscriptionStatus is the current subscription view for a Manager.
type SubscriptionStatus struct {
	Tier                Tier
	FeeKES              int
	LastChargeStatus    string
	LastChargeReference string
}

// GetSubscriptionStatus returns the Manager's current tier, fee, and most
// recent charge status.
func (s *Service) GetSubscriptionStatus(ctx context.Context, managerID uuid.UUID) (SubscriptionStatus, error) {
	var organizationID uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT organization_id
		FROM users
		WHERE id = $1`,
		managerID,
	).Scan(&organizationID); err != nil {
		return SubscriptionStatus{}, fmt.Errorf("lookup manager: %w", err)
	}

	var unitCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM units u
		JOIN properties p ON p.id = u.property_id
		WHERE p.manager_id = $1`,
		managerID,
	).Scan(&unitCount); err != nil {
		return SubscriptionStatus{}, fmt.Errorf("count units: %w", err)
	}

	tier := ComputeTier(unitCount)

	var lastStatus, lastReference string
	err := s.pool.QueryRow(ctx, `
		SELECT status, gateway_reference
		FROM subscription_charges
		WHERE manager_id = $1
		ORDER BY created_at DESC
		LIMIT 1`,
		managerID,
	).Scan(&lastStatus, &lastReference)
	if err != nil && err != pgx.ErrNoRows {
		return SubscriptionStatus{}, fmt.Errorf("lookup last charge: %w", err)
	}

	return SubscriptionStatus{
		Tier:                tier,
		FeeKES:              tier.MonthlyFeeKES(),
		LastChargeStatus:    lastStatus,
		LastChargeReference: lastReference,
	}, nil
}
