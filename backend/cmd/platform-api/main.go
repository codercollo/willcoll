package main

import (
	"context"
	"database/sql"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/internal/shared/jobs"
	"github.com/codercollo/willcoll/backend/internal/shared/jsonlog"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/codercollo/willcoll/backend/internal/shared/vcs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Set at link time by the Makefile and the Dockerfile:
//
//	-ldflags "-X main.version=1.4.0 -X main.buildTime=2026-09-25T10:00:00Z"
//
// Unset (go run, go test) they are empty; buildVersion() then falls back to
// the VCS revision Go stamps into the binary, or "dev".
var (
	releaseVersion = ""
	buildTime      = ""
)

// buildVersion is the running build: the release version injected at link
// time joined with the VCS revision (and "-dirty" for uncommitted changes).
func buildVersion() string { return vcs.Combine(releaseVersion, vcs.Version()) }

// application holds the dependencies shared by every HTTP handler
// (system-design.txt A.3, ADR-0007). platform-api is the ONLY binary that
// creates or destroys a session row, provisions the Super Admin, and owns
// billing — every product API only reads this database.
type application struct {
	// routeGuards records every route's permission or public reason, filled
	// by routes() (internal/shared/auth); the deny-by-default test reads it.
	routeGuards *auth.RouteTable
	// markHandlers makes every response say whether the route's own handler ran
	// (auth.MarkReached). Only the route-matrix tests set it; it is false in every
	// running binary, and a test pins both facts.
	markHandlers   bool
	config         config
	logger         *slog.Logger
	db             *sql.DB
	adminDB        *sql.DB
	models         data.Models
	sessionManager *scs.SessionManager
	// sessionPolicy is the sliding idle window plus absolute cap
	// (internal/shared/auth/sliding.go). The zero value disables both.
	sessionPolicy auth.SlidingPolicy
	mailer        mailer.Mailer
	// payhero is the billing-channel STK client (build-phases.txt 3c-iv-b1).
	// Always non-nil: an unconfigured client fails each call with
	// payhero.ErrNotConfigured rather than the process failing to start.
	payhero stkPusher
	// wg tracks background() goroutines (activation/reset emails) so a
	// graceful shutdown can wait for them (see server.go).
	wg jobs.Group
	// auditFailures counts audit_log write failures in the trailing 24h
	// (build-phases.txt 3f-4).
	auditFailures auditFailureTracker
	// alertFailures counts owner alerts that could not even be queued in the
	// email outbox, trailing 24h (C3.7). Delivery failures are in the outbox
	// itself (email_outbox on the System page).
	alertFailures auditFailureTracker
}

// resp builds the shared JSON-envelope/error-responder layer
// (internal/shared/httpx) bound to this app's logger and flash hook.
func (app *application) responder() httpx.Responder {
	return httpx.Responder{Logger: app.logger, Flash: app.popFlash}
}

func main() {
	cfg := parseConfig()

	logger := jsonlog.New(os.Stdout, jsonlog.LevelForEnv(cfg.env))

	db, err := openDB(cfg, cfg.db.dsn, cfg.db.maxOpenConns)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }() // process is exiting; nothing useful to do on error

	logger.Info("database connection pool established")

	// The willcoll_platform_admin pool (BYPASSRLS), used by /v1/admin/*
	// and Plans (subscription_plans revokes write access from the app
	// role — migration 000013). Admin traffic is one person, so a
	// handful of connections is plenty.
	if cfg.db.adminDSN == "" {
		logger.Error("no admin database DSN: set DB_ADMIN_DSN, or DB_ADMIN_PASSWORD to derive it from DB_DSN")
		os.Exit(1)
	}
	adminDB, err := openDB(cfg, cfg.db.adminDSN, 5)
	if err != nil {
		logger.Error("admin database connection failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = adminDB.Close() }()

	publishMetrics(db)

	mail, err := newMailer(cfg, logger)
	if err != nil {
		logger.Error("mailer setup failed", "error", err)
		os.Exit(1)
	}

	models := data.NewModels(db, adminDB, cfg.db.queryTimeout)
	models.Billing.Env = cfg.env

	app := &application{
		config:         cfg,
		logger:         logger,
		db:             db,
		adminDB:        adminDB,
		models:         models,
		sessionManager: newSessionManager(cfg, db),
		sessionPolicy:  sessionPolicy(cfg),
		mailer:         mail,
		payhero:        payhero.NewClient(cfg.payhero.baseURL, cfg.payhero.apiKey, nil),
	}

	if err := app.provisionAdmin(); err != nil {
		logger.Error("provisioning the Super Admin failed", "error", err)
		os.Exit(1)
	}

	if err := app.serve(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

// openDB opens a pooled connection to Postgres via pgx, applies the
// pool-sizing flags from config, and pings the database so a bad DSN or an
// unreachable server fails fast at startup rather than on the first request.
func openDB(cfg config, dsn string, maxOpenConns int) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(min(cfg.db.maxIdleConns, maxOpenConns))

	duration, err := time.ParseDuration(cfg.db.maxIdleTime)
	if err != nil {
		return nil, err
	}
	db.SetConnMaxIdleTime(duration)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err = db.PingContext(ctx); err != nil {
		_ = db.Close() // already failing: report the original error
		return nil, err
	}

	return db, nil
}

// newSessionManager configures SCS (system-design.txt A.4, ADR-0007):
// server-side sessions in willcoll_platform's sessions table, so revoking
// one is a row delete. The cookie's Domain spans every willcoll host in
// staging/production (config.session.domain), so one login works on
// rentals.*, estates.* and waste.* at once.
func newSessionManager(cfg config, db *sql.DB) *scs.SessionManager {
	// SCS's own IdleTimeout stays 0: it would rewrite the row on every
	// request. Idle expiry and renewal are sessionPolicy's job instead
	// (sliding.go); Lifetime is the absolute cap's backstop.
	sm := auth.NewSessionManager(auth.SessionConfig{
		CookieName: "willcoll_session",
		Lifetime:   cfg.session.lifetime,
		Secure:     cfg.session.secure,
	}, db)
	if cfg.session.domain != "" {
		sm.Cookie.Domain = cfg.session.domain
	}
	return sm
}

// sessionPolicy builds the sliding-session policy from SESSION_IDLE_TIMEOUT
// (renewed at most every idle/4) and SESSION_LIFETIME (the absolute cap).
func sessionPolicy(cfg config) auth.SlidingPolicy {
	return auth.SlidingPolicy{Idle: cfg.session.idleTimeout, MaxLifetime: cfg.session.lifetime}
}

// publishMetrics exposes build and runtime values alongside the request
// counters on /debug/vars.
func publishMetrics(db *sql.DB) {
	expvar.NewString("version").Set(buildVersion())
	expvar.NewString("build_time").Set(buildTime)
	expvar.Publish("goroutines", expvar.Func(func() any { return runtime.NumGoroutine() }))
	expvar.Publish("database", expvar.Func(func() any { return db.Stats() }))
	expvar.Publish("timestamp", expvar.Func(func() any { return time.Now().Unix() }))
}

// newMailer returns the SMTP mailer. Without an SMTP_HOST, development
// falls back to logging emails; any other environment refuses to start,
// since activation and password resets depend on email.
func newMailer(cfg config, logger *slog.Logger) (mailer.Mailer, error) {
	if cfg.smtp.host == "" {
		if cfg.env != "development" {
			return nil, errors.New("SMTP_HOST must be set outside development")
		}
		logger.Warn("SMTP_HOST not set: emails will be logged, not sent")
		return mailer.LogMailer{Logger: logger}, nil
	}

	return mailer.NewSMTP(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender)
}

// provisionAdmin makes sure the single Super Admin from .env (ADMIN_NAME,
// ADMIN_EMAIL, ADMIN_PASSWORD) exists with that password. Without
// ADMIN_EMAIL, admin login stays disabled.
func (app *application) provisionAdmin() error {
	cfg := app.config.admin
	if cfg.email == "" {
		app.logger.Warn("ADMIN_EMAIL not set: no Super Admin, so admin login is disabled")
		return nil
	}

	v := validator.New()
	if data.ValidateAdminCredentials(v, cfg.name, cfg.email, cfg.password); !v.Valid() {
		return fmt.Errorf("invalid Super Admin configuration: %v", v.Errors)
	}

	admin, err := app.models.Admins.Provision(context.Background(), cfg.name, cfg.email, cfg.password)
	if err != nil {
		return err
	}

	app.logger.Info("Super Admin ready", "admin_id", admin.ID, "email", admin.Email)
	return nil
}
