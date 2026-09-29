package main

import (
	"context"
	"database/sql"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/jsonlog"
	"github.com/codercollo/willcoll/backend/internal/mailer"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/internal/vcs"
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

// buildVersion is the running build, e.g. "1.4.0+3f9c2ab1d0e4": the release
// version injected at link time joined with the VCS revision (and "-dirty" for
// uncommitted changes).
func buildVersion() string { return vcs.Combine(releaseVersion, vcs.Version()) }

// application holds the dependencies shared by every HTTP handler
// (system-design.txt 4.1).
type application struct {
	config         config
	logger         *slog.Logger
	db             *sql.DB
	models         data.Models
	sessionManager *scs.SessionManager
	mailer         mailer.Mailer
	payhero        stkPusher
	sms            smsSender
	wg             sync.WaitGroup
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

	// The willcoll_admin pool, for /v1/admin/* only (system-design.txt 3.7).
	// Admin traffic is one person, so a handful of connections is plenty.
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

	payheroClient, smsClient := newPaymentClients(cfg)
	logPaymentConfig(cfg, logger)

	models := data.NewModels(db, adminDB, cfg.db.queryTimeout)
	models.Billing.Env = cfg.env

	app := &application{
		config:         cfg,
		logger:         logger,
		db:             db,
		models:         models,
		sessionManager: newSessionManager(cfg, db),
		mailer:         mail,
		payhero:        payheroClient,
		sms:            smsClient,
	}

	app.publishBusinessMetrics()

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

// newSessionManager configures SCS (system-design.txt 4.4, ADR 0001):
// server-side sessions in the Postgres sessions table (migration 000003),
// so revoking one is a row delete. The cookie is HttpOnly, SameSite=Lax,
// and Secure outside development.
func newSessionManager(cfg config, db *sql.DB) *scs.SessionManager {
	sm := scs.New()
	sm.Store = postgresstore.New(db)
	sm.Lifetime = cfg.session.lifetime
	sm.IdleTimeout = cfg.session.idleTimeout
	sm.Cookie.Name = "willcoll_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = cfg.session.secure
	sm.Cookie.Persist = true
	return sm
}

// publishMetrics exposes build and runtime values alongside the request
// counters on /debug/vars (Greenlight ch.19).
func publishMetrics(db *sql.DB) {
	expvar.NewString("version").Set(buildVersion())
	expvar.NewString("build_time").Set(buildTime)
	expvar.Publish("goroutines", expvar.Func(func() any { return runtime.NumGoroutine() }))
	expvar.Publish("database", expvar.Func(func() any { return db.Stats() }))
	expvar.Publish("timestamp", expvar.Func(func() any { return time.Now().Unix() }))
}

// newMailer returns the SMTP mailer (Greenlight ch.13). Without an
// SMTP_HOST, development falls back to logging emails; any other
// environment refuses to start, since registration and password resets
// depend on email.
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
