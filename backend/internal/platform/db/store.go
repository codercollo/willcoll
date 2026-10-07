// Package db is the repository layer for willcoll_platform: sqlc-generated
// queries (internal/platform/db/sqlc) plus SQLStore, which runs them inside
// tenant-scoped transactions — the same Simple Bank store pattern as
// internal/rentals/db.Store (system-design.txt A.2, A.5). The retry/lock
// orchestration here is intentionally duplicated from rentals/db/store.go
// rather than shared: it's typed against this package's own sqlc.Querier,
// and generifying it is deferred to build-phases.txt's 6-pre step
// (internal/shared/ledgerkit.ExecTx[Q]). What IS shared today are the
// type-free pieces (internal/shared/ledgerkit's retry/timeout constants and
// IsSerializationFailure/AdvisoryKey/Discard helpers) — see that package.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/ledgerkit"
	"github.com/google/uuid"
)

// Store is what the data layer depends on, so tests can swap in the
// gomock-generated mock (internal/platform/db/mock).
type Store interface {
	// Querier runs a single statement outside any tenant transaction. Only
	// for tables without RLS (managers, admins, sessions,
	// subscription_plans, system_metadata): under willcoll_platform_app, a
	// tenant-scoped query issued here fails.
	sqlc.Querier

	// ExecTenantTx runs fn in one SERIALIZABLE transaction whose first
	// statement scopes RLS to tenantID (the manager id). See
	// SQLStore.ExecTenantTx.
	ExecTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q sqlc.Querier) error) error
}

// SQLStore is the Postgres-backed Store.
type SQLStore struct {
	*sqlc.Queries
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(conn *sql.DB) *SQLStore {
	return &SQLStore{Queries: sqlc.New(conn), db: conn}
}

// ExecTenantTx runs fn inside a SERIALIZABLE transaction whose first
// statement is the equivalent of `SET LOCAL app.tenant_id = tenantID`
// (set_config with is_local=true, since SET can't take a bind parameter).
// The setting ends with the transaction, so it can't leak to the next user
// of the pooled connection.
//
// fn is committed if it returns nil and rolled back otherwise. On a
// serialization failure the whole transaction, fn included, is re-run up
// to ledgerkit.MaxSerializationRetries times with jittered exponential
// backoff, so fn must not have side effects outside the transaction.
func (s *SQLStore) ExecTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q sqlc.Querier) error) error {
	if tenantID == uuid.Nil {
		return errors.New("platform/db: ExecTenantTx called without a tenant id")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ledgerkit.DefaultTxTimeout)
		defer cancel()
	}

	backoff := 20 * time.Millisecond

	for attempt := 0; ; attempt++ {
		err := s.execTx(ctx, tenantID, fn)
		if err == nil || !ledgerkit.IsSerializationFailure(err) || attempt == ledgerkit.MaxSerializationRetries {
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
			backoff = min(backoff*2, ledgerkit.MaxBackoff)
		}
	}
}

func (s *SQLStore) execTx(ctx context.Context, tenantID uuid.UUID, fn func(q sqlc.Querier) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return fmt.Errorf("platform/db: setting tenant scope: %w", err)
	}

	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}

	return tx.Commit()
}
