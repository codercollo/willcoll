package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ModuleUsage is one manager's billable count for one product
// (system-design.txt A.7): Rentals counts active units, Estates counts
// units, Waste counts active customers.
type ModuleUsage struct {
	ManagerID     uuid.UUID `json:"manager_id"`
	Product       string    `json:"product"`
	BillableCount int32     `json:"billable_count"`
	ReportedAt    time.Time `json:"reported_at"`
}

func moduleUsageFromRow(r sqlc.ModuleUsage) *ModuleUsage {
	return &ModuleUsage{ManagerID: r.ManagerID, Product: r.Product, BillableCount: r.BillableCount, ReportedAt: r.ReportedAt}
}

// UsageModel is tenant-scoped module_usage access. Get backs pricing
// (3c-iv-b); Upsert is for a product API's own usage reporter
// (build-phases.txt 3e) — nothing in platform-api calls Upsert itself,
// since a product API writes only its own row, in its own tenant
// transaction (system-design.txt E.4).
type UsageModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Get returns tenantID's usage row for product, or ErrRecordNotFound if
// it has never reported one.
func (m UsageModel) Get(ctx context.Context, tenantID uuid.UUID, product string) (*ModuleUsage, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *ModuleUsage
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetModuleUsage(ctx, sqlc.GetModuleUsageParams{ManagerID: tenantID, Product: product})
		if err != nil {
			return notFound(err)
		}
		out = moduleUsageFromRow(row)
		return nil
	})
	return out, err
}

// Upsert records tenantID's current billable count for product. Called by
// a product API on a count change (unit/customer created, archived,
// imported) and from its daily re-count job — never by platform-api.
func (m UsageModel) Upsert(ctx context.Context, tenantID uuid.UUID, product string, count int32) (*ModuleUsage, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *ModuleUsage
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.UpsertModuleUsage(ctx, sqlc.UpsertModuleUsageParams{ManagerID: tenantID, Product: product, BillableCount: count})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
				return ErrRecordNotFound // no such manager
			}
			return err
		}
		out = moduleUsageFromRow(row)
		return nil
	})
	return out, err
}
