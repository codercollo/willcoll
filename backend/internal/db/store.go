// Package db is the repository layer: sqlc-generated queries (internal/db/sqlc)
// plus SQLStore, which runs them inside tenant-scoped transactions
// (the Simple Bank store pattern, system-design.txt 3.7/3.8).
package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Store is what the data layer depends on, so tests can swap in the
// gomock-generated mock (internal/db/mock).
type Store interface {
	// Querier runs a single statement outside any tenant transaction. Only
	// for tables without RLS (managers, tokens, sessions, admins): under
	// the willcoll_app role, a tenant-scoped query issued here fails.
	sqlc.Querier

	// ExecTenantTx runs fn in one SERIALIZABLE transaction whose first
	// statement scopes RLS to tenantID. See SQLStore.ExecTenantTx.
	ExecTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q sqlc.Querier) error) error

	// ExecTenantTxExclusive is ExecTenantTx for work that must not race with
	// itself: it first takes a per-tenant advisory lock named name, before the
	// transaction begins, so concurrent callers with the same tenant and name
	// run one after another instead of colliding and retrying. Used to post
	// payments, where a lost or doubled credit is the one unacceptable
	// outcome. SERIALIZABLE and retry still apply as the backstop.
	ExecTenantTxExclusive(ctx context.Context, tenantID uuid.UUID, name string, fn func(q sqlc.Querier) error) error

	// DatabaseHealth reports database size, connections and WAL-archiver
	// state for the Super Admin (system-design.txt 4.9, 8.3).
	DatabaseHealth(ctx context.Context) (DatabaseHealth, error)
}

// maxSerializationRetries is how many times a transaction is re-run after a
// serialization failure (SQLSTATE 40001) before giving up (system-design.txt 3.8).
const maxSerializationRetries = 3

// maxExclusiveRetries is the allowance for ExecTenantTxExclusive. The advisory
// lock already removes conflicts between one manager's own postings, so what
// is left are false-positive serialization failures caused by other managers'
// concurrent activity on the same tables, which pass on retry. Losing a
// payment to retry exhaustion is the one unacceptable outcome, so this path
// retries much more, with a capped backoff.
const maxExclusiveRetries = 12

// maxBackoff caps the wait between retries.
const maxBackoff = 200 * time.Millisecond

// defaultTxTimeout bounds a tenant transaction whose caller set no deadline.
const defaultTxTimeout = 3 * time.Second

// SQLStore is the Postgres-backed Store.
type SQLStore struct {
	*sqlc.Queries
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *SQLStore {
	return &SQLStore{Queries: sqlc.New(db), db: db}
}

// ExecTenantTx runs fn inside a SERIALIZABLE transaction whose first
// statement is the equivalent of `SET LOCAL app.tenant_id = tenantID`
// (set_config with is_local=true, since SET can't take a bind parameter).
// The setting ends with the transaction, so it can't leak to the next user
// of the pooled connection.
//
// fn is committed if it returns nil and rolled back otherwise. On a
// serialization failure the whole transaction, fn included, is re-run up to
// maxSerializationRetries times with jittered exponential backoff, so fn must not
// have side effects outside the transaction.
func (s *SQLStore) ExecTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q sqlc.Querier) error) error {
	if tenantID == uuid.Nil {
		return errors.New("db: ExecTenantTx called without a tenant id")
	}

	// Backstop for Greenlight ch.8.3: every data-layer caller already sets
	// a deadline, but a transaction must never run unbounded and pin a
	// pooled connection. The deadline covers all retry attempts together.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTxTimeout)
		defer cancel()
	}

	return s.withRetry(ctx, s.db, tenantID, maxSerializationRetries, fn)
}

// txBeginner is what both *sql.DB and *sql.Conn offer.
type txBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// withRetry runs fn in a serializable tenant transaction on b, re-running it
// after a serialization failure.
func (s *SQLStore) withRetry(ctx context.Context, b txBeginner, tenantID uuid.UUID, maxRetries int, fn func(q sqlc.Querier) error) error {
	backoff := 20 * time.Millisecond

	for attempt := 0; ; attempt++ {
		err := execTx(ctx, b, tenantID, fn)
		if err == nil || !isSerializationFailure(err) || attempt == maxRetries {
			return err
		}

		// Jitter: transactions that collided once would otherwise wake at
		// the same instant and collide again, exhausting their retries in
		// lockstep. Waiting a random half to full backoff spreads them out.
		wait := backoff/2 + rand.N(backoff/2+1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

func execTx(ctx context.Context, b txBeginner, tenantID uuid.UUID, fn func(q sqlc.Querier) error) error {
	tx, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return fmt.Errorf("db: setting tenant scope: %w", err)
	}

	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}

	return tx.Commit()
}

// ExecTenantTxExclusive: see Store. The lock is a session-level advisory
// lock held on one dedicated connection for the whole call, and the
// transaction runs on that same connection, so the lock is taken before the
// transaction takes its snapshot. Waiters block in Postgres, not in retries.
func (s *SQLStore) ExecTenantTxExclusive(ctx context.Context, tenantID uuid.UUID, name string, fn func(q sqlc.Querier) error) error {
	if tenantID == uuid.Nil {
		return errors.New("db: ExecTenantTxExclusive called without a tenant id")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTxTimeout)
		defer cancel()
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() // returns the connection to the pool

	key := advisoryKey(tenantID, name)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		discard(conn) // the lock may or may not have been granted: end the session
		return fmt.Errorf("db: taking %q lock: %w", name, err)
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(uctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			discard(conn) // never return a connection still holding a lock to the pool
		}
	}()

	return s.withRetry(ctx, conn, tenantID, maxExclusiveRetries, fn)
}

// discard marks a connection bad so the pool closes it instead of reusing it.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// advisoryKey derives a stable 64-bit lock key for a tenant and a name.
func advisoryKey(tenantID uuid.UUID, name string) int64 {
	sum := sha256.Sum256([]byte("willcoll:" + tenantID.String() + ":" + name))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// DatabaseHealth is a snapshot for the admin system-health and backups
// endpoints. Continuous WAL archiving is the backup mechanism
// (system-design.txt 8.3), so the archiver's last success is the backup
// indicator; the nil fields mean archiving has never run or never failed.
type DatabaseHealth struct {
	DatabaseSizeBytes int64
	Connections       int
	WALArchivedCount  int64
	LastArchivedWAL   *string
	LastArchivedTime  *time.Time
	WALFailedCount    int64
	LastFailedTime    *time.Time

	// LargestTables is the top five tables by total size (table + indexes
	// + TOAST): where the database's disk use is going.
	LargestTables []TableSize
}

// TableSize is one table's total on-disk size.
type TableSize struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// DatabaseHealth reads pg_stat_archiver and the database's size. It's
// hand-written rather than generated because sqlc has no types for the
// pg_stat_* catalog views.
func (s *SQLStore) DatabaseHealth(ctx context.Context) (DatabaseHealth, error) {
	const query = `
		SELECT pg_database_size(current_database()),
		       (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
		       a.archived_count, a.last_archived_wal, a.last_archived_time,
		       a.failed_count, a.last_failed_time
		FROM pg_stat_archiver a`

	var h DatabaseHealth
	err := s.db.QueryRowContext(ctx, query).Scan(
		&h.DatabaseSizeBytes, &h.Connections,
		&h.WALArchivedCount, &h.LastArchivedWAL, &h.LastArchivedTime,
		&h.WALFailedCount, &h.LastFailedTime,
	)
	if err != nil {
		return h, err
	}

	const tablesQuery = `
		SELECT c.relname, pg_total_relation_size(c.oid)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		ORDER BY pg_total_relation_size(c.oid) DESC
		LIMIT 5`

	rows, err := s.db.QueryContext(ctx, tablesQuery)
	if err != nil {
		return h, err
	}
	defer func() { _ = rows.Close() }() // rows.Err() is checked below

	for rows.Next() {
		var t TableSize
		if err := rows.Scan(&t.Name, &t.SizeBytes); err != nil {
			return h, err
		}
		h.LargestTables = append(h.LargestTables, t)
	}
	return h, rows.Err()
}
