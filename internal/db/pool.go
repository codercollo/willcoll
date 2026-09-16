// pgxpool is the single database driver used across this codebase — the
// application query layer is pgx everywhere and is never mixed with
// database/sql + a different driver.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig carries the env-driven pgxpool tuning knobs (LGF §5.3). A zero
// value leaves pgxpool's defaults in place.
type PoolConfig struct {
	MaxConns        int32         // pool size
	MaxConnIdleTime time.Duration // "max idle": time a connection may sit idle
	MaxConnLifetime time.Duration // max time a connection may be reused
}

// NewPool opens a pgxpool connection pool for dsn with pgxpool's default pool
// settings, and verifies it with a round-trip ping before returning it.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return NewPoolWithConfig(ctx, dsn, PoolConfig{})
}

// NewPoolWithConfig opens a pgxpool connection pool for dsn with cfg's non-zero
// pool settings applied, and verifies it with a round-trip ping before
// returning it.
func NewPoolWithConfig(ctx context.Context, dsn string, cfg PoolConfig) (*pgxpool.Pool, error) {
	pgxConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}

	if cfg.MaxConns > 0 {
		pgxConfig.MaxConns = cfg.MaxConns
	}
	if cfg.MaxConnIdleTime > 0 {
		pgxConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.MaxConnLifetime > 0 {
		pgxConfig.MaxConnLifetime = cfg.MaxConnLifetime
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgxConfig)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
