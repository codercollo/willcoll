package main

import (
	"errors"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Middleware chain (system-design.txt A.5), registered in routes.go:
//
//	recoverPanic -> logRequest -> metrics -> loadAndSaveSession ->
//	authenticate -> enableCORS -> [route group guards] -> handler
//
// platform-api is the ONLY binary that creates or destroys a session row
// (ADR-0007): every product API only verifies one via PLATFORM_DB_DSN.
// rateLimit is not global: it applies to the public pay/OTP surface and
// the credential endpoints; PayHero webhooks are exempt.

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
// spoofing the header.
func (app *application) rateLimit() gin.HandlerFunc {
	return httpx.RateLimit(httpx.RateLimiterConfig{
		Enabled: app.config.limiter.enabled,
		RPS:     app.config.limiter.rps,
		Burst:   app.config.limiter.burst,
	}, app.rateLimitExceededResponse)
}

// Session keys (system-design.txt A.1: managers and admins share one
// session table, told apart by a principal-type discriminator).
const (
	sessionKeyPrincipalType = "principal_type"
	sessionKeyPrincipalID   = "principal_id"
	sessionKeyFlash         = "flash"

	principalManager = "manager"
	principalAdmin   = "admin"
	principalStaff   = "staff"

	// sessionLoadedContextKey marks a request whose session was loaded, so
	// helpers can tell whether session data is available.
	sessionLoadedContextKey = "sessionLoaded"

	// flashPutContextKey marks a request that set a flash, which is then
	// held for the next response rather than returned by this one.
	flashPutContextKey = "flashPut"
)

// loadAndSaveSession is scs.SessionManager.LoadAndSave adapted to Gin. It
// loads the session named by the cookie (Domain=.willcoll.co.ke,
// ADR-0007) into the request context, and commits it (writing or clearing
// the cookie) just before the response headers go out, whether the
// handler writes a body or not.
func (app *application) loadAndSaveSession() gin.HandlerFunc {
	sm := app.sessionManager

	return func(c *gin.Context) {
		c.Writer.Header().Add("Vary", "Cookie")

		var token string
		if cookie, err := c.Request.Cookie(sm.Cookie.Name); err == nil {
			token = cookie.Value
		}

		ctx, err := sm.Load(c.Request.Context(), token)
		if err != nil {
			app.serverErrorResponse(c, err)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(sessionLoadedContextKey, true)

		// Idle too long, or past the absolute cap: the session is dead. Delete
		// the row (not just ignore it) so it can't be revived by a policy
		// change, and clear the cookie; the request carries on anonymous.
		if app.sessionPolicy.Expired(sm, ctx) {
			if err := sm.Destroy(ctx); err != nil {
				app.serverErrorResponse(c, err)
				c.Abort()
				return
			}
		}

		sw := &sessionResponseWriter{ResponseWriter: c.Writer}
		sw.commit = func() {
			switch sm.Status(ctx) {
			case scs.Modified:
				token, expiry, err := sm.Commit(ctx)
				if err != nil {
					// Headers haven't gone out yet, but the handler has
					// already chosen its response; log rather than replace it.
					app.logger.Error("committing session", "error", err)
					return
				}
				sm.WriteSessionCookie(ctx, sw.ResponseWriter, token, expiry)
			case scs.Destroyed:
				sm.WriteSessionCookie(ctx, sw.ResponseWriter, "", time.Time{})
			}
		}
		c.Writer = sw

		c.Next()

		// Nothing written (e.g. a bare status): commit before Gin flushes
		// the headers after this middleware returns.
		sw.beforeWrite()
	}
}

// sessionResponseWriter runs commit once, immediately before the response
// headers are flushed.
type sessionResponseWriter struct {
	gin.ResponseWriter
	commit    func()
	committed bool
}

func (w *sessionResponseWriter) beforeWrite() {
	if !w.committed {
		w.committed = true
		w.commit()
	}
}

func (w *sessionResponseWriter) WriteHeaderNow() {
	w.beforeWrite()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *sessionResponseWriter) Write(b []byte) (int, error) {
	w.beforeWrite()
	return w.ResponseWriter.Write(b)
}

func (w *sessionResponseWriter) WriteString(s string) (int, error) {
	w.beforeWrite()
	return w.ResponseWriter.WriteString(s)
}

func (w *sessionResponseWriter) Flush() {
	w.beforeWrite()
	w.ResponseWriter.Flush()
}

// authenticate resolves the session's principal to a manager or admin and
// puts it on the context (system-design.txt A.5). A missing or stale
// session leaves the request anonymous; the route-group guards decide
// whether that's allowed.
func (app *application) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		sm := app.sessionManager

		// The session's step-up window (internal/shared/auth/stepup.go), for
		// requireStepUp and the handlers that gate part of their input.
		if app.sessionLoaded(c) {
			c.Set(auth.StepUpContextKey, sm.GetInt64(ctx, auth.SessionKeyStepUpUntil))
		}

		principalType := sm.GetString(ctx, sessionKeyPrincipalType)
		principalID, err := uuid.Parse(sm.GetString(ctx, sessionKeyPrincipalID))
		if principalType == "" || err != nil {
			contextSetManager(c, data.AnonymousManager)
			c.Next()
			return
		}

		switch principalType {
		case principalManager:
			manager, err := app.models.Managers.Get(ctx, principalID)
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.clearPrincipal(c)
				contextSetManager(c, data.AnonymousManager)
			case err != nil:
				app.serverErrorResponse(c, err)
				c.Abort()
				return
			default:
				contextSetManager(c, manager)
				app.touchSession(c, manager.Status != data.ManagerStatusSuspended)
			}

		case principalAdmin:
			contextSetManager(c, data.AnonymousManager)
			admin, err := app.models.Admins.Get(ctx, principalID)
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.clearPrincipal(c)
			case err != nil:
				app.serverErrorResponse(c, err)
				c.Abort()
				return
			default:
				contextSetAdmin(c, admin)
				app.touchSession(c, true)
			}

		case principalStaff:
			staff, err := app.models.Staff.GetByID(ctx, principalID)
			switch {
			case errors.Is(err, data.ErrRecordNotFound), err == nil && staff.Status != data.StaffActive:
				// Deleted, or disabled since the session was issued
				// (disabling also revokes sessions synchronously, but a
				// session already loaded for this request predates that —
				// fail closed here too).
				app.clearPrincipal(c)
				contextSetManager(c, data.AnonymousManager)
			case err != nil:
				app.serverErrorResponse(c, err)
				c.Abort()
				return
			default:
				manager, err := app.models.Managers.Get(ctx, staff.ManagerID)
				switch {
				case errors.Is(err, data.ErrRecordNotFound):
					app.clearPrincipal(c)
					contextSetManager(c, data.AnonymousManager)
				case err != nil:
					app.serverErrorResponse(c, err)
					c.Abort()
					return
				default:
					// The underlying manager row, same as a manager-principal
					// session would carry: every existing tenant/RLS/
					// activation check keeps working unchanged.
					contextSetManager(c, manager)
					contextSetStaff(c, staff)
					app.touchSession(c, manager.Status != data.ManagerStatusSuspended)
				}
			}

		default:
			app.clearPrincipal(c)
			contextSetManager(c, data.AnonymousManager)
		}

		c.Next()
	}
}

// touchSession slides the session's idle window, at most once per idle/4
// (auth.SlidingPolicy.Touch; the absolute cap is untouched). allowed is false
// for a principal whose firm is suspended: a suspended user is refused by the
// route guards anyway, but must never have their session extended, nor have
// a row re-written by an in-flight request after suspension deleted it.
func (app *application) touchSession(c *gin.Context, allowed bool) {
	if allowed && app.sessionLoaded(c) {
		app.sessionPolicy.Touch(app.sessionManager, c.Request.Context())
	}
}

// enableCORS lets every willcoll app origin (system-design.txt A.4) call
// this API with credentials: 'include', and answers CORS preflight
// requests directly. Untrusted origins get no CORS headers, so the
// browser blocks them.
func (app *application) enableCORS() gin.HandlerFunc {
	return httpx.CORS(app.config.cors.trustedOrigins)
}

// requireActivatedManager guards the manager-facing routes (account, staff,
// billing): a signed-in manager whose account is active.
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
		case manager.Status == data.ManagerStatusSuspended:
			app.accountSuspendedResponse(c)
		case manager.Status != data.ManagerStatusActive:
			app.inactiveAccountResponse(c)
		default:
			c.Next()
			return
		}

		c.Abort()
	}
}

// requirePermission guards one route with one catalog permission
// (internal/shared/auth, docs/security/authorization.md); it replaced
// requireOwner. The permission is resolved per request from the staff row
// authenticate() just loaded, so a demotion takes effect at once.
func (app *application) requirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if app.permitted(c, perm) {
			c.Next()
			return
		}
		if _, isAdmin := contextGetAdmin(c); !isAdmin && contextGetManager(c).IsAnonymous() {
			app.authenticationRequiredResponse(c)
		} else {
			app.logDenied(c, perm)
			app.notPermittedResponse(c)
		}
		c.Abort()
	}
}

// permitted reports whether the request's principal holds perm: a Super
// Admin only admin.*, the firm's own login the owner set plus
// account.password, a staff member their role's set (an unknown role holds
// nothing).
func (app *application) permitted(c *gin.Context, perm string) bool {
	if _, ok := contextGetAdmin(c); ok {
		return auth.AdminPermits(perm)
	}
	if contextGetManager(c).IsAnonymous() {
		return false
	}
	staff := contextGetStaff(c)
	if staff.IsAnonymous() {
		return auth.FirmLoginPermits(perm)
	}
	return auth.StaffPermits(staff.Role, perm)
}

// logDenied records a refused request (actor, permission, route, IP) in the
// application log only: a denied call is not a business event, and writing
// each one to audit_log would let a misbehaving client flood it.
func (app *application) logDenied(c *gin.Context, perm string) {
	actor := "manager"
	if _, ok := contextGetAdmin(c); ok {
		actor = "admin"
	} else if staff := contextGetStaff(c); !staff.IsAnonymous() {
		actor = "staff:" + staff.ID.String()
	}
	app.logger.Info("permission denied", "actor", actor, "permission", perm,
		"method", c.Request.Method, "route", c.FullPath(), "ip", c.ClientIP())
}

// requireAdmin guards /v1/admin/*: only a signed-in Super Admin.
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
