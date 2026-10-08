// Package ledgerkit holds the product-agnostic pieces of the ledger
// transaction machinery: the bits of the Simple Bank SERIALIZABLE-tx +
// 40001-retry pattern that don't depend on any product's generated sqlc
// types. The actual transaction orchestration (ExecTenantTx,
// ExecTenantTxExclusive) stays in each product's own db.Store for now,
// since it's typed against that product's sqlc.Querier — see
// docs/build-phases.txt Phase 5-pre for generifying it once Estates needs
// the same machinery.
package ledgerkit

import (
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxSerializationRetries is how many times a transaction is re-run after a
// serialization failure (SQLSTATE 40001) before giving up (system-design.txt 3.8).
//
// 3 was too tight: several same-tenant runs (e.g. four concurrent billing runs
// of one property) lose SSI conflicts in turn, and on a loaded machine a
// transaction can lose more than three times in a row. With the capped,
// jittered backoff below, 8 attempts wait roughly 1s in total at most, still
// inside DefaultTxTimeout, which bounds the whole call regardless.
const MaxSerializationRetries = 8

// MaxExclusiveRetries is the allowance for a transaction that first takes a
// per-tenant advisory lock. The lock already removes conflicts between one
// tenant's own postings, so what is left are false-positive serialization
// failures caused by other tenants' concurrent activity on the same
// tables, which pass on retry. Losing a payment to retry exhaustion is the
// one unacceptable outcome, so this path retries much more, with a capped
// backoff.
const MaxExclusiveRetries = 12

// MaxBackoff caps the wait between retries.
const MaxBackoff = 200 * time.Millisecond

// DefaultTxTimeout bounds a tenant transaction whose caller set no deadline.
const DefaultTxTimeout = 3 * time.Second

// IsSerializationFailure reports whether err is a Postgres serialization
// failure (SQLSTATE 40001), the signal that a SERIALIZABLE transaction lost
// a conflict and should be retried.
func IsSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// IsRetryable reports whether err means the whole transaction lost a race and
// is safe to re-run from the start: a serialization failure (40001) or a
// detected deadlock (40P01). Postgres has already rolled the transaction back
// in both cases.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

// AdvisoryKey derives a stable 64-bit Postgres advisory-lock key for a
// tenant and a lock name, e.g. AdvisoryKey(tenantID, "payments").
func AdvisoryKey(tenantID uuid.UUID, name string) int64 {
	sum := sha256.Sum256([]byte("willcoll:" + tenantID.String() + ":" + name))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// Discard marks a connection bad so the pool closes it instead of reusing
// it — used when a connection's session state (e.g. a held advisory lock)
// can no longer be trusted.
func Discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
