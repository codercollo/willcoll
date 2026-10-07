package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/google/uuid"
)

// PlatformModel is the service layer behind /v1/admin/*: oversight across
// every tenant (system-design.txt 4.9). Its Store runs as willcoll_admin
// (BYPASSRLS, read-mostly — 3.7). Its only writes are to platform-level
// state (suspending a firm); it never writes a manager's tenant data.
type PlatformModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration

	// HealthTimeout bounds the health-page checks (DatabaseHealth,
	// LastBackup) in place of Timeout when set, so a cold first check is not
	// cut off by the short per-query timeout (HEALTH_TIMEOUT).
	HealthTimeout time.Duration
}

func (m PlatformModel) healthTimeout() time.Duration {
	if m.HealthTimeout > 0 {
		return m.HealthTimeout
	}
	return m.Timeout
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
	ctx, cancel := withTimeout(ctx, m.healthTimeout())
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

// DatabaseHealth returns the database / WAL-archiver snapshot and how long
// the round trip took.
func (m PlatformModel) DatabaseHealth(ctx context.Context) (db.DatabaseHealth, time.Duration, error) {
	ctx, cancel := withTimeout(ctx, m.healthTimeout())
	defer cancel()

	start := time.Now()
	h, err := m.Store.DatabaseHealth(ctx)
	return h, time.Since(start), err
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

// ListTenantIDs returns every tenant that has a property, for the daily
// usage recount sweep. Rentals has no managers table of its own (3i-a).
func (m PlatformModel) ListTenantIDs(ctx context.Context) ([]uuid.UUID, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.AdminListTenantIDs(ctx)
}
