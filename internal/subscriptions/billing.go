package subscriptions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codercollo/willcoll-sys/pkg/intasendclient"
	"github.com/google/uuid"
)

// SubscriptionChargeResult is the result of initiating a subscription charge.
type SubscriptionChargeResult struct {
	ChargeID    uuid.UUID
	CheckoutURL string
	Tier        Tier
	FeeKES      int
}

// InitiateSubscriptionChargeTx sums the Manager's units, computes the tier/fee,
// creates a Willcoll-settling IntaSend checkout, and inserts a PENDING charge.
func (s *Service) InitiateSubscriptionChargeTx(ctx context.Context, managerID uuid.UUID) (SubscriptionChargeResult, error) {
	var organizationID uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT organization_id
		FROM users
		WHERE id = $1`,
		managerID,
	).Scan(&organizationID); err != nil {
		return SubscriptionChargeResult{}, fmt.Errorf("lookup manager: %w", err)
	}

	var unitCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM units u
		JOIN properties p ON p.id = u.property_id
		WHERE p.manager_id = $1`,
		managerID,
	).Scan(&unitCount); err != nil {
		return SubscriptionChargeResult{}, fmt.Errorf("count units: %w", err)
	}

	tier := ComputeTier(unitCount)
	fee := tier.MonthlyFeeKES()

	apiRef := "SUB-" + uuid.NewString()
	checkout, err := s.gateway.CreateCheckout(ctx, intasendclient.CheckoutRequest{
		Currency: "KES",
		Amount:   fmt.Sprintf("%d.00", fee),
		ApiRef:   apiRef,
		Name:     "Willcoll subscription",
	})
	if err != nil {
		return SubscriptionChargeResult{}, fmt.Errorf("create checkout: %w", err)
	}

	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, -1)

	rawPayload, err := json.Marshal(checkout)
	if err != nil {
		return SubscriptionChargeResult{}, fmt.Errorf("marshal checkout payload: %w", err)
	}

	var chargeID uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO subscription_charges (
			organization_id, manager_id, tier, amount_kes, gateway_provider,
			gateway_reference, status, billing_period_start, billing_period_end, raw_payload
		)
		VALUES ($1, $2, $3::subscription_tier, $4, 'intasend', $5, 'PENDING', $6, $7, $8)
		RETURNING id`,
		organizationID, managerID, string(tier), fee, checkout.ID,
		periodStart, periodEnd, json.RawMessage(rawPayload),
	).Scan(&chargeID); err != nil {
		return SubscriptionChargeResult{}, fmt.Errorf("insert subscription charge: %w", err)
	}

	return SubscriptionChargeResult{
		ChargeID:    chargeID,
		CheckoutURL: checkout.URL,
		Tier:        tier,
		FeeKES:      fee,
	}, nil
}
