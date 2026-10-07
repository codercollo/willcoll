package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// manager_modules statuses (system-design.txt A.5).
const (
	ModuleStatusRequested = "requested"
	ModuleStatusTrialing  = "trialing"
	ModuleStatusActive    = "active"
	ModuleStatusSuspended = "suspended"
)

// ManagerModule is a manager's entitlement to one product.
type ManagerModule struct {
	ManagerID   uuid.UUID  `json:"manager_id"`
	Product     string     `json:"product"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	SuspendedAt *time.Time `json:"suspended_at,omitempty"`
	// TrialEndsAt is when this product's free trial ends; nil until the
	// module starts one (build-phases.txt 3f-3 — a trial belongs to the
	// module, not the manager as a whole, so a manager whose Rentals trial
	// has expired still gets a fresh trial when they start Estates).
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
}

func managerModuleFromRow(r sqlc.ManagerModule) *ManagerModule {
	return &ManagerModule{
		ManagerID: r.ManagerID, Product: r.Product, Status: r.Status,
		RequestedAt: r.RequestedAt, ActivatedAt: r.ActivatedAt, SuspendedAt: r.SuspendedAt,
		TrialEndsAt: r.TrialEndsAt,
	}
}

// ModuleModel is tenant-scoped manager_modules access.
type ModuleModel struct {
	Store db.Store

	// Env gates SetTrialEndForDev; set by cmd/devtools only.
	Env string

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Get returns tenantID's entitlement to product, or ErrRecordNotFound if
// it has never been granted one (build-phases.txt 3d's backfill seeds one
// for every existing manager — a missing row for an existing manager is a
// backfill gap worth logging, not a silent allow or deny).
func (m ModuleModel) Get(ctx context.Context, tenantID uuid.UUID, product string) (*ManagerModule, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *ManagerModule
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetManagerModule(ctx, sqlc.GetManagerModuleParams{ManagerID: tenantID, Product: product})
		if err != nil {
			return notFound(err)
		}
		out = managerModuleFromRow(row)
		return nil
	})
	return out, err
}

// StartTrial starts product's free trial for tenantID, days long
// (system-design.txt A.7, build-phases.txt 3f-3): the signup choice, a
// Modules page "start trial", or account activation. A trial starts once
// — calling this again for a product tenantID already has a module row
// for changes nothing and returns that existing row, never pushing the
// trial's end date out further.
func (m ModuleModel) StartTrial(ctx context.Context, tenantID uuid.UUID, product string, days int) (*ManagerModule, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	end := TrialEnd(moneyfmt.Now(), days)
	var out *ManagerModule
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.StartModuleTrial(ctx, sqlc.StartModuleTrialParams{ManagerID: tenantID, Product: product, TrialEndsAt: &end})
		if errors.Is(err, sql.ErrNoRows) {
			// Already has a module row (a retried request, or activation
			// running twice) — read it back rather than treating this as
			// an error or silently doing nothing.
			row, err = q.GetManagerModule(ctx, sqlc.GetManagerModuleParams{ManagerID: tenantID, Product: product})
		}
		if err != nil {
			return err
		}
		out = managerModuleFromRow(row)
		return nil
	})
	return out, err
}

// SetTrialEndForDev force-sets a module's trial end, so the paywall can be
// exercised without waiting on the clock. cmd/devtools' dev/expire-trial
// and dev/reset-trial only; never called from an API, and refuses outside
// development itself (Env), not just via devtools' outer guard.
func (m ModuleModel) SetTrialEndForDev(ctx context.Context, tenantID uuid.UUID, product string, end time.Time) error {
	if m.Env != "development" {
		return ErrNotDevelopment
	}
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		_, err := q.SetModuleTrialEnd(ctx, sqlc.SetModuleTrialEndParams{ManagerID: tenantID, Product: product, TrialEndsAt: &end})
		return notFound(err)
	})
}

// PlatformSubscription is a manager's subscription to one product, as read
// by the requireSubscription shadow gate (build-phases.txt 3e-b). Plain
// fields, deliberately not internal/rentals/data.Subscription — platform
// must not import a product package (project-structure.txt Part D) — so
// rentals-api itself adapts this into the type its own data.ComputeAccess
// expects.
type PlatformSubscription struct {
	ID                 uuid.UUID
	PlanID             uuid.UUID
	Status             string
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
}

// GetSubscription returns tenantID's subscription to product, or
// ErrRecordNotFound if they have none.
func (m ModuleModel) GetSubscription(ctx context.Context, tenantID uuid.UUID, product string) (*PlatformSubscription, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *PlatformSubscription
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetSubscriptionByManagerAndProduct(ctx, sqlc.GetSubscriptionByManagerAndProductParams{ManagerID: tenantID, Product: product})
		if err != nil {
			return notFound(err)
		}
		out = &PlatformSubscription{
			ID: row.ID, PlanID: row.PlanID, Status: row.Status,
			CurrentPeriodStart: row.CurrentPeriodStart, CurrentPeriodEnd: row.CurrentPeriodEnd,
		}
		return nil
	})
	return out, err
}
