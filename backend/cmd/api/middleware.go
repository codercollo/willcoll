package main

import (
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
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
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				c.Writer.Header().Set("Connection", "close")

				err, ok := r.(error)
				if !ok {
					err = fmt.Errorf("%v", r)
				}

				app.serverErrorResponse(c, err)
				c.Abort()
			}
		}()

		c.Next()
	}
}

// logRequest writes one structured log line per request.
func (app *application) logRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		app.logger.Info("request",
			"method", c.Request.Method,
			"uri", redactQueryToken(c.Request.URL),
			"status", c.Writer.Status(),
			"bytes", c.Writer.Size(),
			"duration", time.Since(start).String(),
			"ip", c.ClientIP(),
		)
	}
}

// redactQueryToken returns u's request URI with every "token" query value
// replaced by REDACTED. The PayHero webhook callback URL carries our shared
// secret as ?token=..., and it must never land in a log line verbatim.
func redactQueryToken(u *url.URL) string {
	q := u.Query()
	if _, ok := q["token"]; !ok {
		return u.RequestURI()
	}
	for i := range q["token"] {
		q["token"][i] = "REDACTED"
	}
	cp := *u
	cp.RawQuery = q.Encode()
	return cp.RequestURI()
}

// Request metrics, published on /debug/vars by expvar (system-design.txt
// 4.2). Declared once at package level: expvar names must be unique for
// the life of the process.
var (
	totalRequestsReceived           = expvar.NewInt("requests_received_total")
	totalResponsesSent              = expvar.NewInt("responses_sent_total")
	totalProcessingTimeMicroseconds = expvar.NewInt("processing_time_microseconds")
	totalResponsesSentByStatus      = expvar.NewMap("responses_sent_by_status")
)

// metrics counts requests, responses, processing time and response status
// codes.
func (app *application) metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		totalRequestsReceived.Add(1)

		c.Next()

		totalResponsesSent.Add(1)
		totalProcessingTimeMicroseconds.Add(time.Since(start).Microseconds())
		totalResponsesSentByStatus.Add(strconv.Itoa(c.Writer.Status()), 1)
	}
}

// client tracks one IP's token-bucket limiter and when it was last seen, so
// the cleanup goroutine can evict clients that have gone quiet.
type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// rateLimit is per-IP token-bucket rate limiting. The IP comes from
// c.ClientIP(), which only honours X-Forwarded-For from the configured
// trusted proxies (Caddy); otherwise any caller could dodge the limit by
// spoofing the header. Build it once and share the handler between route
// groups so they draw on the same buckets.
func (app *application) rateLimit() gin.HandlerFunc {
	var (
		mu      sync.Mutex
		clients = make(map[string]*client)
	)

	go func() {
		for {
			time.Sleep(time.Minute)

			mu.Lock()
			for ip, cl := range clients {
				if time.Since(cl.lastSeen) > 3*time.Minute {
					delete(clients, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		if !app.config.limiter.enabled {
			c.Next()
			return
		}

		ip := c.ClientIP()

		mu.Lock()
		cl, found := clients[ip]
		if !found {
			cl = &client{limiter: rate.NewLimiter(rate.Limit(app.config.limiter.rps), app.config.limiter.burst)}
			clients[ip] = cl
		}
		cl.lastSeen = time.Now()
		allowed := cl.limiter.Allow()
		mu.Unlock()

		if !allowed {
			app.rateLimitExceededResponse(c)
			c.Abort()
			return
		}

		c.Next()
	}
}

// Session keys (system-design.txt 3.1: managers and admins share one
// session table, told apart by a principal-type discriminator).
const (
	sessionKeyPrincipalType = "principal_type"
	sessionKeyPrincipalID   = "principal_id"
	sessionKeyFlash         = "flash"

	principalManager = "manager"
	principalAdmin   = "admin"

	// sessionLoadedContextKey marks a request whose session was loaded, so
	// helpers can tell whether session data is available.
	sessionLoadedContextKey = "sessionLoaded"

	// flashPutContextKey marks a request that set a flash, which is then
	// held for the next response rather than returned by this one.
	flashPutContextKey = "flashPut"
)

// loadAndSaveSession is scs.SessionManager.LoadAndSave adapted to Gin. It
// loads the session named by the cookie into the request context, and
// commits it (writing or clearing the cookie) just before the response
// headers go out, whether the handler writes a body or not.
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
// puts it on the context (system-design.txt 4.2 "tenantSession"). A missing
// or stale session leaves the request anonymous; the route-group guards
// decide whether that's allowed.
func (app *application) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		sm := app.sessionManager

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
			}

		default:
			app.clearPrincipal(c)
			contextSetManager(c, data.AnonymousManager)
		}

		c.Next()
	}
}

// enableCORS lets the Nuxt app (a trusted origin) call the API with
// credentials: 'include' (system-design.txt 4.4, 6.4), and answers CORS
// preflight requests directly. Untrusted origins get no CORS headers, so
// the browser blocks them.
func (app *application) enableCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Add("Vary", "Origin")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")

		origin := c.GetHeader("Origin")
		if origin != "" && slices.Contains(app.config.cors.trustedOrigins, origin) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

			if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
				c.Writer.Header().Set("Access-Control-Allow-Methods", "OPTIONS, GET, POST, PUT, PATCH, DELETE")
				// Authorization carries the pay-session Bearer token of the public pay
				// page; without it the browser refuses every cross-origin balances,
				// intent and status call.
				c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				c.Writer.Header().Set("Access-Control-Max-Age", "600")
				c.AbortWithStatus(http.StatusOK)
				return
			}
		}

		c.Next()
	}
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
