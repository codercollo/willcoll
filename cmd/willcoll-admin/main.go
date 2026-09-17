// Command willcoll-admin is the standalone platform admin panel (spec Phase
// 23) — its own binary, own mux, own BYPASSRLS Postgres role
// (platform_admin_panel, migration 000019). It is never wired into
// cmd/willcoll's router or middleware chain.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/codercollo/willcoll-sys/internal/admin"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/money"
)

func main() {
	if err := run(); err != nil {
		slog.Error("willcoll-admin: fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := os.Getenv("ADMIN_DATABASE_DSN")
	username := os.Getenv("ADMIN_USERNAME")
	passwordHash := os.Getenv("ADMIN_PASSWORD_HASH")
	addr := os.Getenv("ADMIN_LISTEN_ADDR")
	if dsn == "" || username == "" || passwordHash == "" {
		return fmt.Errorf("ADMIN_DATABASE_DSN, ADMIN_USERNAME, and ADMIN_PASSWORD_HASH are required")
	}
	if addr == "" {
		addr = ":4100"
	}

	pool, err := db.NewPoolWithConfig(context.Background(), dsn, db.PoolConfig{})
	if err != nil {
		return fmt.Errorf("open db pool: %w", err)
	}
	defer pool.Close()

	moneyService := money.NewService(pool)
	service := admin.NewService(pool, moneyService)
	server := admin.NewServer(service, username, passwordHash, slog.Default())

	slog.Info("willcoll-admin: listening", "addr", addr)
	return http.ListenAndServe(addr, server.Routes())
}
