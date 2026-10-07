package main

import (
	"flag"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
)

// config holds all configuration settings for platform-api. Values are
// sourced from command-line flags, which themselves default to environment
// variables where present — this keeps local dev (.env exported into the
// shell before `go run`) and containerized deploys (env vars only) on one
// code path, same convention as rentals-api (system-design.txt A.3).
type config struct {
	port int
	env  string

	db struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  string
		// queryTimeout bounds each data-layer call.
		queryTimeout time.Duration
		// adminDSN connects as willcoll_platform_admin (BYPASSRLS), used
		// only by the /v1/admin/* handlers.
		adminDSN string
	}

	// admin is the single Super Admin's login, provisioned into the admins
	// table at startup (system-design.txt A.1).
	admin struct {
		name     string
		email    string
		password string
	}

	limiter struct {
		rps     float64
		burst   int
		enabled bool
	}

	// payhero configures the subscription billing channel: Willcoll's own
	// PayHero channel that charges managers their subscription fee
	// (system-design.txt A.6). Not the collections channels — those
	// belong to each product's own payment_channels rows.
	payhero struct {
		baseURL          string
		apiKey           string
		billingChannelID string
		accountID        string

		webhookSecret   string
		webhookIPs      []string
		callbackBaseURL string
	}

	smtp struct {
		host     string
		port     int
		username string
		password string
		sender   string
	}

	cors struct {
		trustedOrigins []string
	}

	// session configures the SCS cookie session shared by every product
	// (system-design.txt A.4, ADR-0007): platform-api is the only binary
	// that creates or destroys a session row.
	session struct {
		lifetime    time.Duration
		idleTimeout time.Duration
		// domain is the cookie's Domain attribute, e.g. ".willcoll.co.ke"
		// in staging/production so every subdomain shares one cookie; empty
		// in local development, where every app runs on localhost and a
		// Domain attribute would break the cookie instead of widening it.
		domain string
		// secure sets the cookie's Secure flag. Caddy terminates TLS in
		// staging/production, so it defaults to true everywhere except
		// development (plain http://localhost).
		secure bool
	}

	// frontendURL is frontend/platform's base URL, used to build the links
	// in activation and password-reset emails.
	frontendURL string

	// productAPIs are the internal base URLs of the product APIs the Super Admin
	// system health page asks for their own report (RENTALS_API_URL, ...). An
	// empty one is reported as "not configured", never as down.
	productAPIs struct {
		rentals string
	}

	// healthTimeout bounds each check on the system health page (the platform
	// database, and each product API), so one slow or dead dependency can
	// never hold the whole page up (HEALTH_TIMEOUT, default 3s).
	healthTimeout time.Duration

	// trialDays is the length of the free trial that starts when a firm
	// activates its account (TRIAL_DAYS, default 7). Still manager-wide,
	// not per-product — build-phases.txt Phase 3 step 3f moves it onto
	// manager_modules.
	trialDays int

	// trustedProxies are the reverse proxies (Caddy) whose X-Forwarded-For
	// header is believed when working out a client's IP for rate limiting.
	trustedProxies []string
}

// envString returns the environment variable named key, or fallback if unset/empty.
func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// parseConfig parses command-line flags (defaulting to environment
// variables) into a config value.
func parseConfig() config {
	var cfg config

	flag.IntVar(&cfg.port, "port", envInt("PORT", 4100), "API server port")
	flag.StringVar(&cfg.env, "env", envString("ENV", "development"), "Environment (development|staging|production)")

	flag.StringVar(&cfg.db.dsn, "db-dsn", envString("DB_DSN", ""), "PostgreSQL DSN (willcoll_platform)")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", envInt("DB_MAX_OPEN_CONNS", 25), "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", envInt("DB_MAX_IDLE_CONNS", 25), "PostgreSQL max idle connections")
	flag.StringVar(&cfg.db.maxIdleTime, "db-max-idle-time", envString("DB_MAX_IDLE_TIME", "15m"), "PostgreSQL max connection idle time")
	flag.StringVar(&cfg.db.adminDSN, "db-admin-dsn", envString("DB_ADMIN_DSN", ""), "PostgreSQL DSN for the willcoll_platform_admin role (default: DB_DSN as willcoll_platform_admin with DB_ADMIN_PASSWORD)")
	flag.DurationVar(&cfg.db.queryTimeout, "db-query-timeout", envDuration("DB_QUERY_TIMEOUT", 3*time.Second), "Timeout for each data-layer database call")

	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", envFloat("LIMITER_RPS", 2), "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", envInt("LIMITER_BURST", 4), "Rate limiter maximum burst")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", envBool("LIMITER_ENABLED", true), "Enable rate limiter")

	flag.StringVar(&cfg.payhero.baseURL, "payhero-base-url", envString("PAYHERO_BASE_URL", ""), "PayHero API base URL")
	flag.StringVar(&cfg.payhero.apiKey, "payhero-api-key", envString("PAYHERO_API_KEY", ""), "PayHero API key")
	flag.StringVar(&cfg.payhero.billingChannelID, "payhero-billing-channel-id", envString("PAYHERO_BILLING_CHANNEL_ID", ""), "PayHero subscription billing channel ID")
	flag.StringVar(&cfg.payhero.accountID, "payhero-account-id", envString("PAYHERO_ACCOUNT_ID", ""), "PayHero account ID")

	var webhookIPs string
	flag.StringVar(&cfg.payhero.webhookSecret, "payhero-webhook-secret", envString("PAYHERO_WEBHOOK_SECRET", ""), "Shared secret PayHero callbacks must carry (?token= or X-Webhook-Token)")
	flag.StringVar(&webhookIPs, "payhero-webhook-ips", envString("PAYHERO_WEBHOOK_IPS", ""), "Callback source IPs/CIDRs allowed to hit the webhooks (space separated)")
	flag.StringVar(&cfg.payhero.callbackBaseURL, "payhero-callback-base-url", envString("PAYHERO_CALLBACK_BASE_URL", ""), "Public API origin PayHero calls back, e.g. https://api.willcoll.co.ke")

	flag.StringVar(&cfg.smtp.host, "smtp-host", envString("SMTP_HOST", ""), "SMTP host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", envInt("SMTP_PORT", 2525), "SMTP port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", envString("SMTP_USERNAME", ""), "SMTP username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", envString("SMTP_PASSWORD", ""), "SMTP password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", envString("SMTP_SENDER", "Willcoll <no-reply@willcoll.co.ke>"), "SMTP sender")

	var trustedOrigins string
	flag.StringVar(&trustedOrigins, "cors-trusted-origins", envString("CORS_TRUSTED_ORIGINS", ""), "Trusted CORS origins: every willcoll app host (space or comma separated; surrounding quotes on an entry are stripped)")

	flag.DurationVar(&cfg.session.lifetime, "session-lifetime", envDuration("SESSION_LIFETIME", 12*time.Hour), "Absolute session lifetime")
	flag.DurationVar(&cfg.session.idleTimeout, "session-idle-timeout", envDuration("SESSION_IDLE_TIMEOUT", 2*time.Hour), "Session idle timeout")
	flag.StringVar(&cfg.session.domain, "session-cookie-domain", envString("SESSION_COOKIE_DOMAIN", ""), "Session cookie Domain attribute, e.g. .willcoll.co.ke (empty in local development)")
	var secureCookie string
	flag.StringVar(&secureCookie, "session-secure", envString("SESSION_SECURE", ""), "Secure session cookie: true|false (default: true unless env=development)")

	flag.StringVar(&cfg.frontendURL, "frontend-url", envString("FRONTEND_URL", "http://localhost:3100"), "frontend/platform base URL for links in emails (activation, password reset)")
	flag.StringVar(&cfg.productAPIs.rentals, "rentals-api-url", envString("RENTALS_API_URL", ""), "rentals-api internal base URL for the system health page, e.g. http://127.0.0.1:4000 (empty: reported as not configured)")
	flag.DurationVar(&cfg.healthTimeout, "health-timeout", envDuration("HEALTH_TIMEOUT", 5*time.Second), "Timeout for each check on the system health page")
	flag.IntVar(&cfg.trialDays, "trial-days", envInt("TRIAL_DAYS", data.DefaultTrialDays), "Length of the free trial, in days, from account activation")

	flag.StringVar(&cfg.admin.name, "admin-name", envString("ADMIN_NAME", "Willcoll Admin"), "Super Admin display name")
	flag.StringVar(&cfg.admin.email, "admin-email", envString("ADMIN_EMAIL", ""), "Super Admin login email")
	// The password is read from the environment only, never a flag: flags
	// show up in process listings.
	cfg.admin.password = os.Getenv("ADMIN_PASSWORD")

	var trustedProxies string
	flag.StringVar(&trustedProxies, "trusted-proxies", envString("TRUSTED_PROXIES", ""), "Trusted reverse-proxy IPs/CIDRs (space separated)")

	flag.Parse()

	cfg.cors.trustedOrigins = parseTrustedOrigins(trustedOrigins)
	cfg.trustedProxies = strings.Fields(trustedProxies)
	cfg.payhero.webhookIPs = strings.Fields(webhookIPs)
	cfg.payhero.callbackBaseURL = strings.TrimRight(cfg.payhero.callbackBaseURL, "/")
	cfg.frontendURL = strings.TrimRight(cfg.frontendURL, "/")
	cfg.productAPIs.rentals = strings.TrimRight(cfg.productAPIs.rentals, "/")

	if cfg.db.adminDSN == "" {
		cfg.db.adminDSN = deriveAdminDSN(cfg.db.dsn, os.Getenv("DB_ADMIN_PASSWORD"))
	}

	cfg.session.secure = cfg.env != "development"
	if b, err := strconv.ParseBool(secureCookie); err == nil {
		cfg.session.secure = b
	}

	return cfg
}

// parseTrustedOrigins splits CORS_TRUSTED_ORIGINS on whitespace OR commas,
// and strips one layer of surrounding quotes from each entry so a quoted
// value doesn't silently fail every CORS check.
func parseTrustedOrigins(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, `"'`)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// deriveAdminDSN builds the willcoll_platform_admin connection string from
// the app's DSN: same host, database and options, with the admin role's
// credentials. Returns "" when there's no password or the DSN isn't a URL.
func deriveAdminDSN(appDSN, adminPassword string) string {
	if adminPassword == "" {
		return ""
	}
	u, err := url.Parse(appDSN)
	if err != nil || u.Scheme == "" {
		return ""
	}
	u.User = url.UserPassword("willcoll_platform_admin", adminPassword)
	return u.String()
}
