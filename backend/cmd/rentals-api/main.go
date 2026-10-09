package main

import (
	"context"
	"database/sql"
	"errors"
	"expvar"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/alexedwards/scs/v2"
	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/internal/shared/jobs"
	"github.com/codercollo/willcoll/backend/internal/shared/jsonlog"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
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

// buildVersion is the running build, e.g. "1.4.0+3f9c2ab1d0e4": the release
// version injected at link time joined with the VCS revision (and "-dirty" for
// uncommitted changes).
func buildVersion() string { return vcs.Combine(releaseVersion, vcs.Version()) }

// application holds the dependencies shared by every HTTP handler
// (system-design.txt 4.1).
type application struct {
	// routeGuards records every route's permission or public reason, filled
	// by routes() (internal/shared/auth); the deny-by-default test reads it.
	routeGuards *auth.RouteTable
	config      config
	logger      *slog.Logger
	db          *sql.DB
	models      data.Models
	mailer      mailer.Mailer
	payhero     stkPusher
	sms         smsSender
	payOTPByIP  ipWindowLimiter
	// otpCeilings records each firm that hit its daily pay-OTP SMS ceiling in
	// the trailing 24h, for the System page.
	otpCeilings firmCeilingTracker
	// wg tracks background() goroutines (emails, SMS, PDF renders) so a
	// graceful shutdown can wait for them (see server.go). jobs.Group,
	// not sync.WaitGroup directly, since background() needs its recover
	// + log behavior too.
	wg jobs.Group
	// usage reports this manager's billable unit count to
	// willcoll_platform (build-phases.txt 3e). nil when PLATFORM_DB_DSN
	// is unset (development only — see config.go) — reportUsage is then a
	// deliberate no-op, not an error: this is additive telemetry, never
	// load-bearing for a request.
	// platform is willcoll_platform's data layer (build-phases.txt 3e):
	// Usage backs the usage reporter, Modules backs the requireModule/
	// requireSubscription shadow gates. nil when PLATFORM_DB_DSN is unset
	// (development only — see config.go); every caller must check for
	// nil and treat it as "no platform connection," not an error — see
	// usage.go and gates.go.
	platform *platformdata.Models
	// platformSessionManager reads (never creates, renews or destroys)
	// willcoll_platform's own session store, for the one case rentals-api
	// needs a platform-only session: a staff login (build-phases.txt
	// 3f-2), which only exists in willcoll_platform — managers and the
	// admin keep signing in against this binary's own local session
	// store (sessionManager), unchanged. nil when app.platform is nil.
	platformSessionManager *scs.SessionManager
	// platformSessionPolicy: idle/absolute expiry checks only (zero value =
	// disabled, as in tests).
	platformSessionPolicy auth.SlidingPolicy
	// usageStatus tracks the reporter's last successful write, read by
	// the Super Admin health page (GET /v1/admin/system/health).
	usageStatus usageStatusTracker
	// auditFailures counts audit_log write failures in the trailing 24h,
	// read by the same health page (build-phases.txt 3f-4).
	auditFailures auditFailureTracker
	// inboxFallbacks counts, in the trailing 24h, every collections webhook
	// that fell back to local-only posting because willcoll_platform's
	// payment_inbox claim could not be reached (build-phases.txt 3g-2) —
	// collections must never stop for this, but a sustained platform outage
	// should be visible on the health page, not silent.
	inboxFallbacks auditFailureTracker
	// alertFailures counts owner alerts that could not be queued in the email
	// outbox, trailing 24h (C3.7). Delivery failures show on the platform
	// System page (email_outbox).
	alertFailures auditFailureTracker
	// unclaimedChannels lists the distinct channels a collections webhook
	// arrived for that platform has no claim on, shown on the health page.
	unclaimedChannels unclaimedChannelTracker
	// unclaimedRecordFailures counts unclaimed payments that could not be
	// written to platform's unclaimed_receipts, trailing 24h: money whose only
	// trace is then the ERROR log.
	unclaimedRecordFailures auditFailureTracker
}

// resp builds the shared JSON-envelope/error-responder layer
// (internal/shared/httpx) bound to this app's logger. A
// value, not a stored field, so every test harness that only sets
// app.logger (not a new field) keeps working unchanged.
func (app *application) responder() httpx.Responder {
	return httpx.Responder{Logger: app.logger}
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

	// The willcoll_platform pools (build-phases.txt 3i-b): REQUIRED in every
	// environment. rentals-api has no identity of its own any more — every
	// manager, staff member and Super Admin signs in through platform-api,
	// and the session, the manager row, the entitlement gates and usage
	// reporting are all read from willcoll_platform — so without it nobody
	// could be authenticated at all. Failing startup beats a binary that
	// answers 401 to everyone.
	if cfg.db.platformDSN == "" {
		logger.Error("PLATFORM_DB_DSN is required: rentals-api authenticates every request against willcoll_platform")
		os.Exit(1)
	}
	platformDB, err := openDB(cfg, cfg.db.platformDSN, 10)
	if err != nil {
		logger.Error("platform database connection failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = platformDB.Close() }()

	// The willcoll_platform_admin (BYPASSRLS) pool: the cross-tenant reads
	// app.platform needs before a tenant is known — resolving a staff
	// session to its staff row, and the payment_inbox sweep.
	if cfg.db.platformAdminDSN == "" {
		logger.Error("no platform admin database DSN: set PLATFORM_DB_ADMIN_DSN, or PLATFORM_DB_ADMIN_PASSWORD to derive it from PLATFORM_DB_DSN")
		os.Exit(1)
	}
	platformAdminDB, err := openDB(cfg, cfg.db.platformAdminDSN, 5)
	if err != nil {
		logger.Error("platform admin database connection failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = platformAdminDB.Close() }()

	platformModels := platformdata.NewModels(platformDB, platformAdminDB, cfg.db.queryTimeout)
	platform := &platformModels
	platformSessionManager := newPlatformSessionManager(cfg, platformDB)
	logger.Info("platform database connection pools established")

	publishMetrics(db)

	mail, err := newMailer(cfg, logger)
	if err != nil {
		logger.Error("mailer setup failed", "error", err)
		os.Exit(1)
	}

	payheroClient, smsClient := newPaymentClients(cfg)
	logPaymentConfig(cfg, logger)

	models := data.NewModels(db, adminDB, cfg.db.queryTimeout)
	models.Platform.HealthTimeout = cfg.healthTimeout
	models.PayAccess.FirmDailyOTPs = cfg.pay.firmDailyOTPs

	app := &application{
		config:                 cfg,
		logger:                 logger,
		db:                     db,
		models:                 models,
		mailer:                 mail,
		payhero:                payheroClient,
		sms:                    smsClient,
		platform:               platform,
		platformSessionManager: platformSessionManager,
		platformSessionPolicy:  platformSessionPolicy(cfg),
	}

	app.publishBusinessMetrics()

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

// platformSessionCookieName is willcoll_platform's own session cookie
// (cmd/platform-api's newSessionManager) — the one a staff login sets.
const platformSessionCookieName = "willcoll_session"

// newPlatformSessionManager wires a second SCS manager, read-only in
// practice (build-phases.txt 3f-2's authenticate() only ever calls Load,
// never RenewToken/Commit/Destroy on it): rentals-api verifies a staff
// session against willcoll_platform, but never creates, renews or deletes
// one — only platform-api may (ADR-0007).
func newPlatformSessionManager(cfg config, platformDB *sql.DB) *scs.SessionManager {
	return auth.NewSessionManager(auth.SessionConfig{
		CookieName: platformSessionCookieName,
		Lifetime:   cfg.session.lifetime,
		Secure:     cfg.session.secure,
	}, platformDB)
}

// platformSessionPolicy mirrors platform-api's sliding-session limits so an
// idle or over-age session is anonymous here too. rentals-api only CHECKS
// it (Expired); it never renews: a Touch would need a Commit, and only
// platform-api writes the session. The frontend's activity ping keeps the
// session alive (build-phases.txt, "Sliding sessions").
func platformSessionPolicy(cfg config) auth.SlidingPolicy {
	return auth.SlidingPolicy{Idle: cfg.session.idleTimeout, MaxLifetime: cfg.session.lifetime}
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
