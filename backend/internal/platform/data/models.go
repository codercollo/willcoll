// Package data is the service layer for willcoll_platform, in the same
// Greenlight-Models shape as internal/rentals/data: handlers call
// app.models.Managers.Get(...) and never see SQL. It holds identity and
// billing — managers, staff, tokens, subscriptions, modules — the state
// every product API reads over its PLATFORM_DB_DSN pool (system-design.txt
// A.2, A.3). Scope in this step (build-phases.txt 3b): the identity core
// (Admins, Managers, Tokens) only — staff, subscriptions, manager_modules
// and the rest grow their own models in later sub-steps (3c, 3e-3h) as the
// handlers that need them move here.
package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
)

var (
	// ErrRecordNotFound is returned when a query expected exactly one row
	// (Get) and found none.
	ErrRecordNotFound = errors.New("record not found")

	// ErrEditConflict is returned by an Update when the row's version no
	// longer matches the version the caller read (optimistic concurrency).
	ErrEditConflict = errors.New("edit conflict")
)

// Postgres error codes this package translates into sentinel errors.
const pgUniqueViolation = "23505"
const pgForeignKeyViolation = "23503"

// DefaultQueryTimeout bounds every model method when no timeout is
// configured, so a stuck query can't hold a pooled connection forever.
const DefaultQueryTimeout = 3 * time.Second

// Models is the aggregate every product API's platform pool wires up.
type Models struct {
	Admins   AdminModel
	Managers ManagerModel
	Tokens   TokenModel
	// Plans is admin-only plan CRUD (list/create/update/archive/restore),
	// run on the willcoll_platform_admin pool: subscription_plans revokes
	// INSERT/UPDATE/DELETE from willcoll_platform_app (migration 000013).
	Plans PlanModel
	// Usage is module_usage access (build-phases.txt 3c-iv-a): Get backs
	// pricing (3c-iv-b); Upsert is for each product API's own usage
	// reporter (3e) — not called from platform-api itself.
	Usage UsageModel
	// Modules is manager_modules + per-product subscription access
	// (build-phases.txt 3e-b): backs each product API's requireModule/
	// requireSubscription shadow gates.
	Modules ModuleModel
	// Billing is the one subscriptions/invoices implementation every
	// product API delegates to (build-phases.txt 3c-iv-b1). Env must be set
	// by the caller (platform-api's own cfg.env) after NewModels/
	// NewModelsFromStore returns — it can't be threaded through those
	// constructors without changing every other model's signature for a
	// field only Billing needs.
	Billing BillingModel
	// Staff is sub-accounts under a manager (build-phases.txt 3f-1):
	// owner-driven management runs tenant-scoped on the app pool; login
	// and activation are cross-tenant, on the admin pool.
	Staff StaffModel
	// Audit is the append-only audit trail (build-phases.txt 3f-4).
	Audit AuditModel
	// Channels is payment_channels: one PayHero channel id belongs to
	// exactly one product+owner (build-phases.txt 3g-1).
	Channels ChannelModel
	// Inbox is payment_inbox's claim/post dedup (build-phases.txt 3g-2).
	Inbox InboxModel
	// Oversight is the Super Admin's cross-tenant manager/subscription
	// view and the suspend/reinstate status moves (build-phases.txt
	// 3i-b). Admin pool only.
	Oversight OversightModel
}

// NewModels wires the models to conn (the willcoll_platform_app pool) and
// adminConn (willcoll_platform_admin, BYPASSRLS, used only by Plans). Each
// method call is bounded by timeout (zero means DefaultQueryTimeout).
func NewModels(conn, adminConn *sql.DB, timeout time.Duration) Models {
	return newModels(db.NewStore(conn), db.NewStore(adminConn), timeout)
}

// NewModelsFromStore wires every model, Plans included, to one store;
// tests pass a mock.
func NewModelsFromStore(store db.Store, timeout time.Duration) Models {
	return newModels(store, store, timeout)
}

func newModels(store, adminStore db.Store, timeout time.Duration) Models {
	usage := UsageModel{Store: store, Timeout: timeout}
	return Models{
		Admins:   AdminModel{Store: store, Timeout: timeout},
		Managers: ManagerModel{Store: store, Timeout: timeout},
		Tokens:   TokenModel{Store: store, Timeout: timeout},
		Plans:    PlanModel{Store: adminStore, Timeout: timeout},
		Usage:    usage,
		Modules:  ModuleModel{Store: store, Timeout: timeout},
		Billing:  BillingModel{Store: store, AdminStore: adminStore, Usage: usage, Timeout: timeout},
		Staff:    StaffModel{Store: store, AdminStore: adminStore, Timeout: timeout},
		Audit:    AuditModel{Store: store, Timeout: timeout},
		Channels: ChannelModel{Store: store, Timeout: timeout},
		Inbox:    InboxModel{Store: store, Timeout: timeout},

		Oversight: OversightModel{Store: adminStore, Timeout: timeout},
	}
}

// notFound maps sql.ErrNoRows from a single-row query to ErrRecordNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRecordNotFound
	}
	return err
}

// editConflict maps sql.ErrNoRows from a version-checked UPDATE ... RETURNING
// to ErrEditConflict: either the version moved on, or the row is gone.
func editConflict(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEditConflict
	}
	return err
}

// withTimeout bounds ctx by d, or DefaultQueryTimeout when d is zero.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, d)
}
