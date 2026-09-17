// Package admin is the platform admin panel's backend (spec Phase 23) — a
// single hardcoded operator, a handful of deliberately-chosen screens
// (23.4-23.6), nothing else. It runs as its own binary (cmd/willcoll-admin)
// against its own BYPASSRLS Postgres role (platform_admin_panel, migration
// 000019), never internal/api's pool. No tenant/ledger/unit browsing lives
// here — new screens get added deliberately, one at a time (spec §23.7).
package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns every read/write this panel is allowed to make.
type Service struct {
	pool  *pgxpool.Pool
	money *money.Service
}

func NewService(pool *pgxpool.Pool, moneyService *money.Service) *Service {
	return &Service{pool: pool, money: moneyService}
}

// Organization is one row of the 23.4 platform health list.
type Organization struct {
	ID               uuid.UUID
	Name             string
	SubscriptionTier string
	BillingStatus    string
	UnitCount        int
	CreatedAt        time.Time
}

func (s *Service) ListOrganizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.subscription_tier::text, o.billing_status::text, o.created_at,
		       COALESCE(COUNT(u.id), 0) AS unit_count
		FROM organizations o
		LEFT JOIN properties p ON p.organization_id = o.id
		LEFT JOIN units u ON u.property_id = p.id
		GROUP BY o.id
		ORDER BY o.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	out := make([]Organization, 0)
	for rows.Next() {
		var o Organization
		var unitCount int64
		if err := rows.Scan(&o.ID, &o.Name, &o.SubscriptionTier, &o.BillingStatus, &o.CreatedAt, &unitCount); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		o.UnitCount = int(unitCount)
		out = append(out, o)
	}
	return out, rows.Err()
}

var validTiers = map[string]bool{"starter": true, "growth": true, "professional": true, "enterprise": true}
var validBillingStatuses = map[string]bool{"active": true, "past_due": true, "suspended": true}

// UpdateOrganization is the ONLY write path this whole panel exposes besides
// its own audit trail (spec 23.6) — subscription_tier and billing_status,
// nothing else. actorLabel identifies the admin operator (spec §23.1's
// hardcoded username) for the audit_log row this always writes.
func (s *Service) UpdateOrganization(ctx context.Context, orgID uuid.UUID, tier, billingStatus, actorLabel string) error {
	if !validTiers[tier] {
		return fmt.Errorf("invalid subscription_tier %q", tier)
	}
	if !validBillingStatuses[billingStatus] {
		return fmt.Errorf("invalid billing_status %q", billingStatus)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE organizations
		SET subscription_tier = $2::subscription_tier, billing_status = $3::org_billing_status, updated_at = now()
		WHERE id = $1`,
		orgID, tier, billingStatus,
	); err != nil {
		return fmt.Errorf("update organization: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (organization_id, action, entity_type, entity_id, metadata, source)
		VALUES ($1, 'organization.updated', 'organization', $1, jsonb_build_object(
			'subscription_tier', $2::text, 'billing_status', $3::text, 'operator', $4::text
		), 'platform_admin')`,
		orgID, tier, billingStatus, actorLabel,
	); err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}

	return tx.Commit(ctx)
}

// AddonSubscriber is one row of the 23.4 "who's paying for the premium
// tier" list — active verified_property_score subscribers only.
type AddonSubscriber struct {
	OrganizationID   uuid.UUID
	OrganizationName string
	MonthlyFeeKES    float64
	ActivatedAt      time.Time
}

func (s *Service) ListScoreAddonSubscribers(ctx context.Context) ([]AddonSubscriber, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT oa.organization_id, o.name, oa.monthly_fee_kes, oa.activated_at
		FROM organization_addons oa
		JOIN organizations o ON o.id = oa.organization_id
		WHERE oa.addon_key = 'verified_property_score' AND oa.status = 'active'
		ORDER BY oa.activated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list score addon subscribers: %w", err)
	}
	defer rows.Close()

	out := make([]AddonSubscriber, 0)
	for rows.Next() {
		var a AddonSubscriber
		if err := rows.Scan(&a.OrganizationID, &a.OrganizationName, &a.MonthlyFeeKES, &a.ActivatedAt); err != nil {
			return nil, fmt.Errorf("scan addon subscriber: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AuditEntry is one cross-org audit_log row (spec 23.5) — same shape
// internal/api/handlers_audit.go exposes per-org, just without the
// organization_id filter that handler's RLS-scoped tx applies.
type AuditEntry struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	PropertyID     *uuid.UUID
	ActorID        *uuid.UUID
	Action         string
	EntityType     string
	Source         string
	CreatedAt      time.Time
}

func (s *Service) ListAuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, property_id, actor_id, action, entity_type, source, created_at
		FROM audit_log
		ORDER BY created_at DESC
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	defer rows.Close()

	out := make([]AuditEntry, 0)
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.PropertyID, &a.ActorID, &a.Action, &a.EntityType, &a.Source, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// StuckGatewayTransaction is a payment_gateway_transactions row still
// PENDING past the given threshold (spec 23.5).
type StuckGatewayTransaction struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	GatewayRef     string
	Channel        string
	Amount         string
	ReceivedAt     time.Time
}

func (s *Service) ListStuckGatewayTransactions(ctx context.Context, olderThan time.Duration) ([]StuckGatewayTransaction, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, gateway_reference, channel, amount::text, received_at
		FROM payment_gateway_transactions
		WHERE status = 'PENDING' AND received_at < now() - $1::interval
		ORDER BY received_at`,
		olderThan.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list stuck gateway transactions: %w", err)
	}
	defer rows.Close()

	out := make([]StuckGatewayTransaction, 0)
	for rows.Next() {
		var t StuckGatewayTransaction
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.GatewayRef, &t.Channel, &t.Amount, &t.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan stuck transaction: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LedgerDrift reuses money.Service.Reconcile's own detection query (spec
// 23.5 — "don't reimplement it") rather than restating the drift math here.
func (s *Service) LedgerDrift(ctx context.Context) (money.ReconcileReport, error) {
	return s.money.Reconcile(ctx, nil)
}
