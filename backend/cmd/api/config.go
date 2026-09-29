package main

import (
	"flag"
	"github.com/codercollo/willcoll/backend/internal/data"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// config holds all configuration settings for the application. Values are
// sourced from command-line flags, which themselves default to environment
// variables where present — this keeps local dev (.env exported into the
// shell before `go run`) and containerized deploys (env vars only) on one
// code path, per system-design.txt 4.1 & 8.1.
type config struct {
	port int
	env  string

	db struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  string
		// queryTimeout bounds each data-layer call (Greenlight ch.8.3).
		queryTimeout time.Duration
		// adminDSN connects as willcoll_admin (BYPASSRLS), used only by the
		// /v1/admin/* handlers (system-design.txt 3.7).
		adminDSN string
	}

	// admin is the single Super Admin's login, provisioned into the admins
	// table at startup (system-design.txt 1.1).
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

	payhero struct {
		baseURL              string
		apiKey               string
		collectionsChannelID string
		billingChannelID     string
		// accountID is the PayHero account a newly registered channel is
		// created under (their "Register Payment Channel" account_id field).
		accountID string

		// Callback authentication (see payhero.VerifyConfig): whatever is
		// set is enforced; with neither, webhooks are refused.
		webhookSecret string
		webhookIPs    []string
		// callbackBaseURL is this API public origin, used to build the
		// callback URL sent with each STK push.
		callbackBaseURL string
	}

	// pay configures the public tenant payment surface.
	pay struct {
		// sessionSecret signs pay-session tokens and hashes OTP codes. Empty
		// disables the public pay endpoints.
		sessionSecret string
		otpTTL        time.Duration
		sessionTTL    time.Duration
		intentTTL     time.Duration
	}

	// trialDays is the length of the free trial that starts when a firm
	// activates its account (TRIAL_DAYS, default 7).
	trialDays int

	// subscriptionGate turns the trial/paywall on (SUBSCRIPTION_GATE, default
	// true). Off only for local experiments: it is what makes a firm buy a plan.
	subscriptionGate bool

	// qr configures lease-bound unit QR codes. baseURL is the stable, branded
	// public origin the printed sticker encodes (https://willcoll.app), never the
	// raw API origin: stickers outlive infrastructure changes. The reverse proxy
	// routes /q/* on that domain to this API's /q/:token. Empty disables code
	// generation (fail closed).
	qr struct {
		baseURL string
	}

	africastalking struct {
		apiKey   string
		username string
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

	// session configures the SCS cookie session shared by managers and
	// admins (system-design.txt 4.4).
	session struct {
		lifetime    time.Duration
		idleTimeout time.Duration
		// secure sets the cookie's Secure flag. Caddy terminates TLS in
		// staging/production, so it defaults to true everywhere except
		// development (plain http://localhost).
		secure bool
	}

	// frontendURL is the Nuxt app's base URL, used to build the links in
	// activation and password-reset emails (system-design.txt 6.1).
	frontendURL string

	// trustedProxies are the reverse proxies (Caddy) whose X-Forwarded-For
	// header is believed when working out a client's IP for rate limiting.
	// Empty means trust none and use the TCP peer address.
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

	flag.IntVar(&cfg.port, "port", envInt("PORT", 4000), "API server port")
	flag.StringVar(&cfg.env, "env", envString("ENV", "development"), "Environment (development|staging|production)")

	flag.StringVar(&cfg.db.dsn, "db-dsn", envString("DB_DSN", ""), "PostgreSQL DSN")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", envInt("DB_MAX_OPEN_CONNS", 25), "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", envInt("DB_MAX_IDLE_CONNS", 25), "PostgreSQL max idle connections")
	flag.StringVar(&cfg.db.maxIdleTime, "db-max-idle-time", envString("DB_MAX_IDLE_TIME", "15m"), "PostgreSQL max connection idle time")
	flag.StringVar(&cfg.db.adminDSN, "db-admin-dsn", envString("DB_ADMIN_DSN", ""), "PostgreSQL DSN for the willcoll_admin role (default: DB_DSN as willcoll_admin with DB_ADMIN_PASSWORD)")
	flag.DurationVar(&cfg.db.queryTimeout, "db-query-timeout", envDuration("DB_QUERY_TIMEOUT", 3*time.Second), "Timeout for each data-layer database call")

	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", envFloat("LIMITER_RPS", 2), "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", envInt("LIMITER_BURST", 4), "Rate limiter maximum burst")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", envBool("LIMITER_ENABLED", true), "Enable rate limiter")

	flag.StringVar(&cfg.payhero.baseURL, "payhero-base-url", envString("PAYHERO_BASE_URL", ""), "PayHero API base URL")
	flag.StringVar(&cfg.payhero.apiKey, "payhero-api-key", envString("PAYHERO_API_KEY", ""), "PayHero API key")
	flag.StringVar(&cfg.payhero.collectionsChannelID, "payhero-collections-channel-id", envString("PAYHERO_COLLECTIONS_CHANNEL_ID", ""), "PayHero collections channel ID")
	flag.StringVar(&cfg.payhero.billingChannelID, "payhero-billing-channel-id", envString("PAYHERO_BILLING_CHANNEL_ID", ""), "PayHero billing channel ID")
	flag.StringVar(&cfg.payhero.accountID, "payhero-account-id", envString("PAYHERO_ACCOUNT_ID", ""), "PayHero account ID new payment channels are registered under")

	var webhookIPs string
	flag.StringVar(&cfg.payhero.webhookSecret, "payhero-webhook-secret", envString("PAYHERO_WEBHOOK_SECRET", ""), "Shared secret PayHero callbacks must carry (?token= or X-Webhook-Token)")
	flag.StringVar(&webhookIPs, "payhero-webhook-ips", envString("PAYHERO_WEBHOOK_IPS", ""), "Callback source IPs/CIDRs allowed to hit the webhooks (space separated)")
	flag.StringVar(&cfg.payhero.callbackBaseURL, "payhero-callback-base-url", envString("PAYHERO_CALLBACK_BASE_URL", ""), "Public API origin PayHero calls back, e.g. https://api.willcoll.app")

	// The secret is read from the environment only, never a flag.
	cfg.pay.sessionSecret = os.Getenv("PAY_SESSION_SECRET")
	flag.DurationVar(&cfg.pay.otpTTL, "pay-otp-ttl", envDuration("PAY_OTP_TTL", 5*time.Minute), "How long an SMS one-time code stays valid")
	flag.DurationVar(&cfg.pay.sessionTTL, "pay-session-ttl", envDuration("PAY_SESSION_TTL", 15*time.Minute), "How long a verified pay session stays valid")
	flag.DurationVar(&cfg.pay.intentTTL, "pay-intent-ttl", envDuration("PAY_INTENT_TTL", 10*time.Minute), "How long a payment intent waits for its STK push to complete")

	flag.StringVar(&cfg.africastalking.apiKey, "africastalking-api-key", envString("AFRICASTALKING_API_KEY", ""), "Africa's Talking API key")
	flag.StringVar(&cfg.africastalking.username, "africastalking-username", envString("AFRICASTALKING_USERNAME", ""), "Africa's Talking username")

	flag.StringVar(&cfg.smtp.host, "smtp-host", envString("SMTP_HOST", ""), "SMTP host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", envInt("SMTP_PORT", 2525), "SMTP port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", envString("SMTP_USERNAME", ""), "SMTP username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", envString("SMTP_PASSWORD", ""), "SMTP password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", envString("SMTP_SENDER", "Willcoll <no-reply@willcoll.app>"), "SMTP sender")

	var trustedOrigins string
	flag.StringVar(&trustedOrigins, "cors-trusted-origins", envString("CORS_TRUSTED_ORIGINS", ""), "Trusted CORS origins (space or comma separated; surrounding quotes on an entry are stripped)")

	flag.DurationVar(&cfg.session.lifetime, "session-lifetime", envDuration("SESSION_LIFETIME", 12*time.Hour), "Absolute session lifetime")
	flag.DurationVar(&cfg.session.idleTimeout, "session-idle-timeout", envDuration("SESSION_IDLE_TIMEOUT", 2*time.Hour), "Session idle timeout")
	var secureCookie string
	flag.StringVar(&secureCookie, "session-secure", envString("SESSION_SECURE", ""), "Secure session cookie: true|false (default: true unless env=development)")

	flag.BoolVar(&cfg.subscriptionGate, "subscription-gate", envBool("SUBSCRIPTION_GATE", true), "Enforce the free trial and the subscription paywall")
	flag.IntVar(&cfg.trialDays, "trial-days", envInt("TRIAL_DAYS", data.DefaultTrialDays), "Length of the free trial, in days, from account activation")
	flag.StringVar(&cfg.qr.baseURL, "qr-base-url", envString("QR_BASE_URL", ""), "Public branded origin encoded in unit QR codes, e.g. https://willcoll.app (required to generate codes)")
	cfg.qr.baseURL = strings.TrimRight(strings.TrimSpace(cfg.qr.baseURL), "/")
	flag.StringVar(&cfg.frontendURL, "frontend-url", envString("FRONTEND_URL", "http://localhost:3000"), "Frontend base URL for links in emails only (activation, password reset) — never used for a browser redirect, e.g. /q/:token, since a phone scanning a QR code can't reach the frontend dev server's own address")

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

	if cfg.db.adminDSN == "" {
		cfg.db.adminDSN = deriveAdminDSN(cfg.db.dsn, os.Getenv("DB_ADMIN_PASSWORD"))
	}

	cfg.session.secure = cfg.env != "development"
	if b, err := strconv.ParseBool(secureCookie); err == nil {
		cfg.session.secure = b
	}

	return cfg
}

// parseTrustedOrigins splits CORS_TRUSTED_ORIGINS on whitespace OR commas
// (a comma-separated value, quoted the way many .env tools and shells write
// it, is easy to end up with by mistake — e.g. CORS_TRUSTED_ORIGINS="a, b"),
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

// deriveAdminDSN builds the willcoll_admin connection string from the app's
// DSN: same host, database and options, with the admin role's credentials.
// Returns "" when there's no password or the DSN isn't a URL.
func deriveAdminDSN(appDSN, adminPassword string) string {
	if adminPassword == "" {
		return ""
	}
	u, err := url.Parse(appDSN)
	if err != nil || u.Scheme == "" {
		return ""
	}
	u.User = url.UserPassword("willcoll_admin", adminPassword)
	return u.String()
}
