package main

import (
	"errors"
	"net/http"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Middleware chain (system-design.txt 4.2), registered in routes.go:
//
//	recoverPanic -> logRequest -> metrics -> loadAndSaveSession ->
//	authenticate -> enableCORS -> [route group guards] -> handler
//
// The spec's "tenantSession" step is loadAndSaveSession + authenticate:
// they resolve the SCS session to a manager or admin. The RLS transaction
// it describes is opened per data-layer call by db.Store.ExecTenantTx,
// keyed on the tenant id authenticate puts on the context. rateLimit is
// not global: per the spec it applies to the public pay/OTP surface (and
// the credential endpoints), and PayHero webhooks are exempt.

// recoverPanic converts a panic in a later handler into a Greenlight-style
// JSON 500 instead of Gin's plain-text abort, and sets Connection: close so
// the server drops a connection whose state is now unknown.
func (app *application) recoverPanic() gin.HandlerFunc {
	return httpx.RecoverPanic(app.serverErrorResponse)
}

// logRequest writes one structured log line per request.
func (app *application) logRequest() gin.HandlerFunc {
	return httpx.LogRequest(app.logger)
}

// metrics counts requests, responses, processing time and response status
// codes.
func (app *application) metrics() gin.HandlerFunc {
	return httpx.Metrics()
}

// rateLimit is per-IP token-bucket rate limiting. The IP comes from
// c.ClientIP(), which only honours X-Forwarded-For from the configured
// trusted proxies (Caddy); otherwise any caller could dodge the limit by
// spoofing the header. Build it once and share the handler between route
// groups so they draw on the same buckets.
func (app *application) rateLimit() gin.HandlerFunc {
	return httpx.RateLimit(httpx.RateLimiterConfig{
		Enabled: app.config.limiter.enabled,
		RPS:     app.config.limiter.rps,
		Burst:   app.config.limiter.burst,
	}, app.rateLimitExceededResponse)
}

// payRateLimits returns the pay surface's two limiters. Behind carrier-grade
// NAT one public IP is many tenants, so the per-IP cap is loose (a safety net)
// and status polling is bucketed by intent id instead. The status limiter
// falls back to the IP only when the URL carries no id.
func (app *application) payRateLimits() (perIP, perIntent gin.HandlerFunc) {
	perIP = httpx.RateLimit(httpx.RateLimiterConfig{
		Enabled: app.config.limiter.enabled,
		RPS:     app.config.pay.ipRPS,
		Burst:   app.config.pay.ipBurst,
	}, app.rateLimitExceededResponse)
	perIntent = httpx.RateLimitBy(httpx.RateLimiterConfig{
		Enabled: app.config.limiter.enabled,
		RPS:     app.config.pay.statusRPS,
		Burst:   app.config.pay.statusBurst,
	}, func(c *gin.Context) string {
		if id := c.Param("id"); id != "" {
			return "intent:" + id
		}
		return "ip:" + c.ClientIP()
	}, app.rateLimitExceededResponse)
	return perIP, perIntent
}

// Session principal types, as platform-api writes them into the shared
// willcoll_platform session row (ADR-0007).
const (
	sessionKeyPrincipalType = "principal_type"
	sessionKeyPrincipalID   = "principal_id"

	principalManager = "manager"
	principalAdmin   = "admin"
	principalStaff   = "staff"
)

// authenticate resolves the willcoll_platform session cookie to a manager,
// staff member or Super Admin and puts it on the context (system-design.txt
// A.5; build-phases.txt 3i-b). platform-api is the ONLY binary that signs
// anyone in or out (ADR-0007): this one reads the shared session store
// and NEVER calls RenewToken/Commit/Destroy on it. The manager's and the
// staff member's status is read from willcoll_platform on every request,
// so a suspension takes effect on the very next call even for a session
// that somehow survived revocation.
//
// A missing or stale session leaves the request anonymous; the route-group
// guards decide whether that's allowed. When the platform can't be read
// the answer is 503 — never a silent anonymous pass-through that a retry
// would then treat as "signed out".
func (app *application) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !app.resolvePlatformPrincipal(c) {
			return // already answered (503)
		}
		c.Next()
	}
}

// resolvePlatformPrincipal sets the request's principal. It returns false
// only if it aborted the request with a 503.
func (app *application) resolvePlatformPrincipal(c *gin.Context) bool {
	contextSetManager(c, platformdata.AnonymousManager)

	if app.platform == nil || app.platformSessionManager == nil {
		return true // no platform connection: nobody can be signed in
	}
	cookie, err := c.Request.Cookie(platformSessionCookieName)
	if err != nil || cookie.Value == "" {
		return true
	}

	sm := app.platformSessionManager
	sessionCtx, err := sm.Load(c.Request.Context(), cookie.Value)
	if err != nil {
		return app.signInCheckUnavailable(c, "loading platform session", err)
	}
	if app.platformSessionPolicy.Expired(sm, sessionCtx) {
		return true // idle too long or past the absolute cap: anonymous (never destroyed here)
	}
	// The step-up window platform-api opened on this session, if any (D5,
	// internal/shared/auth/stepup.go). Read-only like everything else here.
	c.Set(auth.StepUpContextKey, sm.GetInt64(sessionCtx, auth.SessionKeyStepUpUntil))
	principalID, err := uuid.Parse(sm.GetString(sessionCtx, sessionKeyPrincipalID))
	if err != nil {
		return true
	}

	ctx := c.Request.Context()
	switch sm.GetString(sessionCtx, sessionKeyPrincipalType) {
	case principalManager:
		manager, err := app.platform.Managers.Get(ctx, principalID)
		switch {
		case errors.Is(err, platformdata.ErrRecordNotFound):
		case err != nil:
			return app.signInCheckUnavailable(c, "resolving manager session", err)
		default:
			contextSetManager(c, manager)
		}

	case principalAdmin:
		admin, err := app.platform.Admins.Get(ctx, principalID)
		switch {
		case errors.Is(err, platformdata.ErrRecordNotFound):
		case err != nil:
			return app.signInCheckUnavailable(c, "resolving admin session", err)
		default:
			contextSetAdmin(c, admin)
		}

	case principalStaff:
		staff, err := app.platform.Staff.GetByID(ctx, principalID)
		switch {
		case errors.Is(err, platformdata.ErrRecordNotFound):
			return true
		case err != nil:
			return app.signInCheckUnavailable(c, "resolving staff session", err)
		case staff.Status != platformdata.StaffActive:
			// Disabled since the session was issued (disabling also
			// revokes sessions synchronously, but a session already
			// loaded for this request predates that) — fail closed.
			return true
		}
		manager, err := app.platform.Managers.Get(ctx, staff.ManagerID)
		switch {
		case errors.Is(err, platformdata.ErrRecordNotFound):
		case err != nil:
			return app.signInCheckUnavailable(c, "resolving staff's manager", err)
		default:
			// The underlying manager row, same as a manager session would
			// carry: every tenant/RLS/activation check applies unchanged.
			contextSetManager(c, manager)
			contextSetStaff(c, staff)
			// Property scope (D2): carried in the request context so the
			// database layer applies it to every query, not just to routes
			// that remember to check (internal/shared/auth/scope.go).
			c.Request = c.Request.WithContext(auth.WithPropertyScope(ctx, staff.PropertyScope(platformdata.ProductRentals)))
		}
	}
	return true
}

func (app *application) signInCheckUnavailable(c *gin.Context, what string, err error) bool {
	app.logger.Error(what, "error", err)
	app.errorResponse(c, http.StatusServiceUnavailable, "sign-in check is temporarily unavailable; try again shortly")
	c.Abort()
	return false
}

// enableCORS lets the Nuxt app (a trusted origin) call the API with
// credentials: 'include' (system-design.txt 4.4, 6.4), and answers CORS
// preflight requests directly. Untrusted origins get no CORS headers, so
// the browser blocks them.
func (app *application) enableCORS() gin.HandlerFunc {
	return httpx.CORS(app.config.cors.trustedOrigins)
}

// requireActivatedManager guards the manager dashboard routes (system-design.txt
// 4.5): a signed-in manager whose account is active. Every query those
// handlers make then runs inside that manager's RLS scope.
func (app *application) requireActivatedManager() gin.HandlerFunc {
	return func(c *gin.Context) {
		manager := contextGetManager(c)

		switch {
		case manager.IsAnonymous():
			if _, isAdmin := contextGetAdmin(c); isAdmin {
				app.notPermittedResponse(c)
			} else {
				app.authenticationRequiredResponse(c)
			}
		case manager.Status == platformdata.ManagerStatusSuspended:
			app.accountSuspendedResponse(c)
		case manager.Status != platformdata.ManagerStatusActive:
			app.inactiveAccountResponse(c)
		default:
			c.Next()
			return
		}

		c.Abort()
	}
}

// requireAdmin guards /v1/admin/* (system-design.txt 4.5): only a signed-in
// Super Admin.
func (app *application) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := contextGetAdmin(c); ok {
			c.Next()
			return
		}

		if !contextGetManager(c).IsAnonymous() {
			app.notPermittedResponse(c)
		} else {
			app.authenticationRequiredResponse(c)
		}
		c.Abort()
	}
}
