package data

import (
	"context"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// OversightModel is the service layer behind platform-api's /v1/admin/
// managers and /v1/admin/subscriptions (build-phases.txt 3i-b; moved from
// cmd/rentals-api's PlatformModel). Its Store MUST be the
// willcoll_platform_admin pool (BYPASSRLS): it reads across every tenant.
// It reads willcoll_platform only — firm, modules, subscriptions, staff
// and the usage counts each product reported into module_usage — never a
// product database. Its only writes are the suspend/reinstate status
// moves (willcoll_platform_admin holds UPDATE on managers, migration
// 000013).
type OversightModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// ManagerOverview is one firm as the Super Admin sees it. The embedded
// Manager and the Subscription/UnitCount fields keep the shape the old
// rentals-api route returned, so the admin frontend needs minimal change;
// Modules, Subscriptions, Usage and StaffCount are the platform-wide
// additions. There is deliberately no property_count: properties live in
// the Rentals database, which this model never reads.
type ManagerOverview struct {
	*Manager
	// Subscription is the firm's latest subscription across products, or
	// nil if it has none (the old single-subscription shape).
	Subscription *SubscriptionState `json:"subscription"`
	// UnitCount is Rentals' reported billable count (module_usage).
	UnitCount  int `json:"unit_count"`
	StaffCount int `json:"staff_count"`

	// Detail is filled by Get and by List: every module, per-product
	// subscription and reported usage row the firm has.
	Modules       []*ManagerModule       `json:"modules"`
	Subscriptions []*ProductSubscription `json:"subscriptions"`
	Usage         []*ModuleUsage         `json:"usage"`
}

// SubscriptionState is the status of a firm's latest subscription.
type SubscriptionState struct {
	Status           string    `json:"status"`
	CurrentPeriodEnd time.Time `json:"current_period_end"`
}

// ProductSubscription is one of a firm's per-product subscriptions.
type ProductSubscription struct {
	Subscription
	PlanName string `json:"plan_name"`
}

// SubscriptionOverview is one subscription with its firm and plan. Same
// shape the old rentals-api route returned, plus product.
type SubscriptionOverview struct {
	ID                 uuid.UUID      `json:"id"`
	ManagerID          uuid.UUID      `json:"manager_id"`
	FirmName           string         `json:"firm_name"`
	Product            string         `json:"product"`
	PlanID             uuid.UUID      `json:"plan_id"`
	PlanName           string         `json:"plan_name"`
	PlanPrice          moneyfmt.Money `json:"plan_price"`
	BillingInterval    string         `json:"billing_interval"`
	Status             string         `json:"status"`
	CurrentPeriodStart time.Time      `json:"current_period_start"`
	CurrentPeriodEnd   time.Time      `json:"current_period_end"`
}

// ListManagers pages every firm, optionally only those with status.
func (m OversightModel) ListManagers(ctx context.Context, status string, filters Filters) ([]*ManagerOverview, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.OversightListManagers(ctx, sqlc.OversightListManagersParams{
		Status:     optional(status),
		Sort:       filters.Sort,
		PageLimit:  int32(filters.limit()),
		PageOffset: int32(filters.offset()),
	})
	if err != nil {
		return nil, Metadata{}, err
	}

	totalRecords := 0
	managers := make([]*ManagerOverview, 0, len(rows))
	for _, row := range rows {
		totalRecords = int(row.TotalRecords)
		managers = append(managers, managerOverview(row.Manager, row.SubscriptionStatus, row.SubscriptionPeriodEnd, row.StaffCount, row.UnitCount))
	}
	if err := m.attachDetail(ctx, managers); err != nil {
		return nil, Metadata{}, err
	}

	return managers, calculateMetadata(totalRecords, filters.Page, filters.PageSize), nil
}

// GetManager returns one firm's overview, or ErrRecordNotFound.
func (m OversightModel) GetManager(ctx context.Context, id uuid.UUID) (*ManagerOverview, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.OversightGetManager(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	o := managerOverview(row.Manager, row.SubscriptionStatus, row.SubscriptionPeriodEnd, row.StaffCount, row.UnitCount)
	if err := m.attachDetail(ctx, []*ManagerOverview{o}); err != nil {
		return nil, err
	}
	return o, nil
}

// attachDetail fills Modules, Subscriptions and Usage for a page of firms
// in three queries, not three per firm.
func (m OversightModel) attachDetail(ctx context.Context, managers []*ManagerOverview) error {
	if len(managers) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(managers))
	byID := make(map[uuid.UUID]*ManagerOverview, len(managers))
	for i, o := range managers {
		ids[i] = o.ID
		byID[o.ID] = o
	}

	modules, err := m.Store.OversightListModules(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range modules {
		o := byID[r.ManagerID]
		o.Modules = append(o.Modules, managerModuleFromRow(r))
	}

	subs, err := m.Store.OversightListManagerSubscriptions(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range subs {
		o := byID[r.Subscription.ManagerID]
		o.Subscriptions = append(o.Subscriptions, &ProductSubscription{
			Subscription: subscriptionFromRow(r.Subscription),
			PlanName:     r.PlanName,
		})
	}

	usage, err := m.Store.OversightListUsage(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range usage {
		o := byID[r.ManagerID]
		o.Usage = append(o.Usage, moduleUsageFromRow(r))
	}
	return nil
}

// ListStaffIDs returns every staff account of managerID, whatever its
// status, so suspending the firm can sign each of them out.
func (m OversightModel) ListStaffIDs(ctx context.Context, managerID uuid.UUID) ([]uuid.UUID, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.OversightListStaffIDs(ctx, managerID)
}

// SuspendManager suspends a firm, active or still pending. From the next
// request on, its sessions and its staff's are refused
// (requireActivatedManager checks status on every request, in every
// binary); the caller also destroys them outright. Suspending an already
// suspended firm is a no-op. Returns ErrRecordNotFound for an unknown id.
func (m OversightModel) SuspendManager(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	current, err := m.Store.OversightGetManager(ctx, id)
	if err != nil {
		return notFound(err)
	}
	if current.Manager.Status == ManagerStatusSuspended {
		return nil
	}
	return m.setStatus(ctx, id, current.Manager.Status, ManagerStatusSuspended)
}

// ReinstateManager lifts a suspension. A firm that had activated goes back
// to active; one suspended before it ever activated goes back to pending,
// so reinstating never skips activation. Reinstating a firm that isn't
// suspended is a no-op. Returns ErrRecordNotFound for an unknown id.
func (m OversightModel) ReinstateManager(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	current, err := m.Store.OversightGetManager(ctx, id)
	if err != nil {
		return notFound(err)
	}
	if current.Manager.Status != ManagerStatusSuspended {
		return nil
	}

	to := ManagerStatusPending
	if current.Manager.ActivatedAt != nil {
		to = ManagerStatusActive
	}
	return m.setStatus(ctx, id, ManagerStatusSuspended, to)
}

// setStatus moves a manager from one status to another. If the status
// changed underneath us (another admin action), that's an edit conflict.
func (m OversightModel) setStatus(ctx context.Context, id uuid.UUID, from, to string) error {
	_, err := m.Store.SetManagerStatus(ctx, sqlc.SetManagerStatusParams{ID: id, FromStatus: from, ToStatus: to})
	return editConflict(err)
}

// ListSubscriptions pages every firm's subscriptions, optionally only
// those of one product and/or status.
func (m OversightModel) ListSubscriptions(ctx context.Context, product, status string, filters Filters) ([]*SubscriptionOverview, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.OversightListSubscriptions(ctx, sqlc.OversightListSubscriptionsParams{
		Status:     optional(status),
		Product:    optional(product),
		Sort:       filters.Sort,
		PageLimit:  int32(filters.limit()),
		PageOffset: int32(filters.offset()),
	})
	if err != nil {
		return nil, Metadata{}, err
	}

	totalRecords := 0
	subs := make([]*SubscriptionOverview, 0, len(rows))
	for _, row := range rows {
		totalRecords = int(row.TotalRecords)
		subs = append(subs, &SubscriptionOverview{
			ID:                 row.Subscription.ID,
			ManagerID:          row.Subscription.ManagerID,
			FirmName:           row.FirmName,
			Product:            row.Subscription.Product,
			PlanID:             row.Subscription.PlanID,
			PlanName:           row.PlanName,
			PlanPrice:          row.PlanPrice,
			BillingInterval:    row.PlanBillingInterval,
			Status:             row.Subscription.Status,
			CurrentPeriodStart: row.Subscription.CurrentPeriodStart,
			CurrentPeriodEnd:   row.Subscription.CurrentPeriodEnd,
		})
	}

	return subs, calculateMetadata(totalRecords, filters.Page, filters.PageSize), nil
}

func managerOverview(row sqlc.Manager, subStatus string, subPeriodEnd time.Time, staff, units int32) *ManagerOverview {
	o := &ManagerOverview{
		Manager:       managerFromRow(row),
		StaffCount:    int(staff),
		UnitCount:     int(units),
		Modules:       []*ManagerModule{},
		Subscriptions: []*ProductSubscription{},
		Usage:         []*ModuleUsage{},
	}
	// '' is the query's "no subscription" sentinel.
	if subStatus != "" {
		o.Subscription = &SubscriptionState{Status: subStatus, CurrentPeriodEnd: subPeriodEnd}
	}
	return o
}

// optional maps "" to nil, for sqlc.narg filters.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
