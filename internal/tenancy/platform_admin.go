package tenancy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WithPlatformAdmin runs fn inside a transaction whose role is switched to the
// internal-only platform_admin Postgres role (granted BYPASSRLS, spec §1a).
// It is the ONLY code path that can read or write across Organization
// boundaries, and exists solely for cross-tenant platform jobs (§12 tier
// billing rollup, money.Service.Reconcile's cross-org drift check). It is never
// reachable from internal/api — no handler constructs or calls it. Every use is
// logged.
func (s *Service) WithPlatformAdmin(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin platform admin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// SET LOCAL ROLE platform_admin grants this transaction the BYPASSRLS role
	// for its duration only; the caller's role is restored on commit/rollback.
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE platform_admin"); err != nil {
		return fmt.Errorf("set platform_admin role: %w", err)
	}

	s.logger.Info("platform admin transaction started")

	if err := fn(tx); err != nil {
		s.logger.Error("platform admin transaction failed", "error", err)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit platform admin transaction", "error", err)
		return fmt.Errorf("commit platform admin transaction: %w", err)
	}

	s.logger.Info("platform admin transaction committed")
	return nil
}
