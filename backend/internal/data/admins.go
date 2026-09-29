package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Admin is the Super Admin (system-design.txt 1.1): owns the platform,
// sits outside every tenant, and never touches a manager's ledger.
type Admin struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Password  password  `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// ValidateAdminCredentials checks the configured Super Admin login. The bar
// is higher than for managers: this account can suspend any firm.
func ValidateAdminCredentials(v *validator.Validator, name, email, password string) {
	v.Check(name != "", "ADMIN_NAME", "must be provided")
	v.Check(validator.Matches(email, validator.EmailRX), "ADMIN_EMAIL", "must be a valid email address")
	v.Check(len(password) >= 12, "ADMIN_PASSWORD", "must be at least 12 bytes long")
	v.Check(len(password) <= 72, "ADMIN_PASSWORD", "must not be more than 72 bytes long")
}

// AdminModel is the service layer for the Super Admin's own account. The
// admins table has no RLS; these queries run on the API's app pool.
type AdminModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Get returns the admin with the given id, or ErrRecordNotFound.
func (m AdminModel) Get(ctx context.Context, id uuid.UUID) (*Admin, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetAdmin(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	return adminFromRow(row), nil
}

// GetByEmail returns the admin with the given email, or ErrRecordNotFound.
func (m AdminModel) GetByEmail(ctx context.Context, email string) (*Admin, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetAdminByEmail(ctx, email)
	if err != nil {
		return nil, notFound(err)
	}
	return adminFromRow(row), nil
}

// Provision makes sure the one Super Admin has this name, email and
// password (the credentials live in .env: ADMIN_NAME / ADMIN_EMAIL /
// ADMIN_PASSWORD). It's a no-op when they already match, so the bcrypt
// hash isn't rewritten on every start. Changing ADMIN_PASSWORD rotates the
// password; changing ADMIN_EMAIL re-points the same admin row (migration
// 000023 allows exactly one). Validate with ValidateAdminCredentials first.
func (m AdminModel) Provision(ctx context.Context, name, email, plaintext string) (*Admin, error) {
	existing, err := m.GetByEmail(ctx, email)
	switch {
	case err == nil:
		if existing.Name == name {
			if ok, err := existing.Password.Matches(plaintext); err == nil && ok {
				return existing, nil
			}
		}
	case !errors.Is(err, ErrRecordNotFound):
		return nil, err
	}

	var pw password
	if err := pw.Set(plaintext); err != nil {
		return nil, err
	}

	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.UpsertAdmin(ctx, sqlc.UpsertAdminParams{Name: name, Email: email, PasswordHash: pw.hash})
	if err != nil {
		return nil, err
	}
	return adminFromRow(row), nil
}

func adminFromRow(row sqlc.Admin) *Admin {
	return &Admin{
		ID:        row.ID,
		Name:      row.Name,
		Email:     row.Email,
		Password:  password{hash: row.PasswordHash},
		CreatedAt: row.CreatedAt,
	}
}

// PlatformModel is the service layer behind /v1/admin/*: oversight across
// every tenant (system-design.txt 4.9). Its Store runs as willcoll_admin
// (BYPASSRLS, read-mostly — 3.7). Its only writes are to platform-level
// state (suspending a firm); it never writes a manager's tenant data.
type PlatformModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// ManagerOverview is one firm as the Super Admin sees it.
type ManagerOverview struct {
	*Manager
	// Subscription is the firm's latest subscription, or nil if it has none.
	Subscription  *SubscriptionState `json:"subscription"`
	PropertyCount int                `json:"property_count"`
	UnitCount     int                `json:"unit_count"`
}

// SubscriptionState is the status of a firm's latest subscription.
type SubscriptionState struct {
	Status           string    `json:"status"`
	CurrentPeriodEnd time.Time `json:"current_period_end"`
}

// SubscriptionOverview is one subscription with its firm and plan.
type SubscriptionOverview struct {
	ID                 uuid.UUID      `json:"id"`
	ManagerID          uuid.UUID      `json:"manager_id"`
	FirmName           string         `json:"firm_name"`
	PlanID             uuid.UUID      `json:"plan_id"`
	PlanName           string         `json:"plan_name"`
	PlanPrice          moneyfmt.Money `json:"plan_price"`
	BillingInterval    string         `json:"billing_interval"`
	Status             string         `json:"status"`
	CurrentPeriodStart time.Time      `json:"current_period_start"`
	CurrentPeriodEnd   time.Time      `json:"current_period_end"`
}

// Subscription statuses (system-design.txt 3.6).
const (
	SubscriptionStatusTrialing  = "trialing"
	SubscriptionStatusActive    = "active"
	SubscriptionStatusPastDue   = "past_due"
	SubscriptionStatusCancelled = "cancelled"
)

// ListManagers pages every firm, optionally only those with status.
func (m PlatformModel) ListManagers(ctx context.Context, status string, filters Filters) ([]*ManagerOverview, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.AdminListManagers(ctx, sqlc.AdminListManagersParams{
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
		managers = append(managers, managerOverview(row.Manager, row.SubscriptionStatus, row.SubscriptionPeriodEnd, row.PropertyCount, row.UnitCount))
	}

	return managers, calculateMetadata(totalRecords, filters.Page, filters.PageSize), nil
}

// GetManager returns one firm's overview, or ErrRecordNotFound.
func (m PlatformModel) GetManager(ctx context.Context, id uuid.UUID) (*ManagerOverview, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AdminGetManager(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	return managerOverview(row.Manager, row.SubscriptionStatus, row.SubscriptionPeriodEnd, row.PropertyCount, row.UnitCount), nil
}

// SuspendManager suspends a firm, active or still pending. From the next
// request on, its sessions are refused (requireActivatedManager checks
// status on every request); the caller also destroys them outright.
// Suspending an already suspended firm is a no-op. Returns
// ErrRecordNotFound for an unknown id.
func (m PlatformModel) SuspendManager(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	current, err := m.Store.AdminGetManager(ctx, id)
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
func (m PlatformModel) ReinstateManager(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	current, err := m.Store.AdminGetManager(ctx, id)
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
func (m PlatformModel) setStatus(ctx context.Context, id uuid.UUID, from, to string) error {
	_, err := m.Store.SetManagerStatus(ctx, sqlc.SetManagerStatusParams{ID: id, FromStatus: from, ToStatus: to})
	return editConflict(err)
}

// BackupRecord is the last base backup as recorded by
// scripts/db_backup_verify.sh from the backup repository (pgBackRest or
// S3), which, unlike Postgres, knows its size.
type BackupRecord struct {
	CompletedAt time.Time `json:"completed_at"`
	SizeBytes   int64     `json:"size_bytes"`
	Tool        string    `json:"tool"`
	Label       string    `json:"label,omitempty"`
	VerifiedAt  time.Time `json:"verified_at"`
}

// LastBackup returns the recorded last base backup, or nil when the
// backup script has never recorded one.
func (m PlatformModel) LastBackup(ctx context.Context) (*BackupRecord, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetSystemMetadata(ctx, "last_backup")
	if err != nil {
		if errors.Is(notFound(err), ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var record BackupRecord
	if err := json.Unmarshal(row.Value, &record); err != nil {
		return nil, fmt.Errorf("decoding system_metadata last_backup: %w", err)
	}
	return &record, nil
}

// ListSubscriptions pages every firm's subscriptions, optionally only
// those with status.
func (m PlatformModel) ListSubscriptions(ctx context.Context, status string, filters Filters) ([]*SubscriptionOverview, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.AdminListSubscriptions(ctx, sqlc.AdminListSubscriptionsParams{
		Status:     optional(status),
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

// DatabaseHealth returns the database / WAL-archiver snapshot and how long
// the round trip took.
func (m PlatformModel) DatabaseHealth(ctx context.Context) (db.DatabaseHealth, time.Duration, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	start := time.Now()
	h, err := m.Store.DatabaseHealth(ctx)
	return h, time.Since(start), err
}

func managerOverview(row sqlc.Manager, subStatus string, subPeriodEnd time.Time, properties, units int32) *ManagerOverview {
	o := &ManagerOverview{
		Manager:       managerFromRow(row),
		PropertyCount: int(properties),
		UnitCount:     int(units),
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

// ReviewQueueDepth counts, across every manager, the payments waiting on a
// person: Unmatched (no unit could be found) and Unconfirmed (placed by the
// engine on a guess). Counts only; used for the /debug/vars gauges.
type ReviewQueueDepth struct {
	Unmatched   int64 `json:"unmatched"`
	Unconfirmed int64 `json:"unconfirmed"`
}

func (m PlatformModel) ReviewQueueDepth(ctx context.Context) (ReviewQueueDepth, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AdminReviewQueueDepth(ctx)
	if err != nil {
		return ReviewQueueDepth{}, err
	}
	return ReviewQueueDepth{Unmatched: row.Unmatched, Unconfirmed: row.Unconfirmed}, nil
}
