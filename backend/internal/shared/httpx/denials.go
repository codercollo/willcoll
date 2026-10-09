package httpx

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Request ids and denied-request logging (docs/security/c3-spec.md C3.5).
//
// A refused request is not a business event, so it goes to the application
// log and never to audit_log (a misbehaving client must not be able to fill
// the audit trail). The line carries who, what they were refused, where and
// from which address, plus the request id that ties it to the access log and
// to the response header the client saw. It never carries a body, a cookie, a
// header or a raw URL (path tokens such as /q/:token stay as the route
// template).

const (
	// RequestIDHeader is echoed on every response.
	RequestIDHeader = "X-Request-Id"

	requestIDKey      = "request_id"
	permissionKey     = "denied_permission"
	scopeDeniedKey    = "denied_out_of_scope"
	defaultDenialsMin = 10
	maxTrackedActors  = 10000
	denialWindow      = time.Minute
)

// RequestID gives every request a fresh id (never one the client chose), on
// the context for log lines and on the response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set(requestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestIDFrom returns the request's id, or "" outside the RequestID chain.
func RequestIDFrom(c *gin.Context) string { return c.GetString(requestIDKey) }

// NoteDeniedPermission records the permission a request was refused for, for
// the denial log line. The refusal itself is the caller's to write.
func NoteDeniedPermission(c *gin.Context, perm string) { c.Set(permissionKey, perm) }

// MarkScopeDenied marks a 404 as a property-scope denial (the resource exists
// but is outside the caller's scope), so it is logged like a 403 even though
// the client sees an ordinary not-found.
func MarkScopeDenied(c *gin.Context) { c.Set(scopeDeniedKey, true) }

// DenialConfig configures DenialLog.
type DenialConfig struct {
	Logger *slog.Logger
	// Actor names the caller for the log: "manager:<id>", "staff:<id>",
	// "admin:<id>" or "anonymous".
	Actor func(c *gin.Context) string
	// Permission resolves the permission guarding the matched route (from the
	// route table); a permission noted by NoteDeniedPermission wins.
	Permission func(c *gin.Context) string
	// PerActorPerMinute caps denial lines per actor (per IP for anonymous
	// callers) per minute. Zero means 10.
	PerActorPerMinute int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

type denialBucket struct {
	windowStart time.Time
	logged      int
	suppressed  int
}

// DenialLog logs 401, 403 and scope-404 responses as structured warnings,
// rate limited per actor: after PerActorPerMinute lines in a window the rest
// are counted, and the count is reported on the first line of the next window
// (suppressed_before), so a flood is visible without filling the log.
// Place it after RequestID and before authenticate's outer middleware: it
// reads the status and the principal once the handlers have run.
func DenialLog(cfg DenialConfig) gin.HandlerFunc {
	if cfg.PerActorPerMinute <= 0 {
		cfg.PerActorPerMinute = defaultDenialsMin
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	var (
		mu      sync.Mutex
		buckets = map[string]*denialBucket{}
	)

	// admit reports whether a line for key may be written now, and how many
	// earlier lines for it were suppressed.
	admit := func(key string) (ok bool, suppressedBefore int) {
		mu.Lock()
		defer mu.Unlock()
		t := now()
		b := buckets[key]
		if b == nil {
			if len(buckets) >= maxTrackedActors {
				for k, old := range buckets {
					if t.Sub(old.windowStart) >= denialWindow {
						delete(buckets, k)
					}
				}
				if len(buckets) >= maxTrackedActors {
					key = "overflow" // every actor past the cap shares one budget
					b = buckets[key]
				}
			}
			if b == nil {
				b = &denialBucket{windowStart: t}
				buckets[key] = b
			}
		}
		if t.Sub(b.windowStart) >= denialWindow {
			suppressedBefore = b.suppressed
			*b = denialBucket{windowStart: t}
		}
		if b.logged >= cfg.PerActorPerMinute {
			b.suppressed++
			return false, 0
		}
		b.logged++
		return true, suppressedBefore
	}

	return func(c *gin.Context) {
		c.Next()

		status := c.Writer.Status()
		var reason string
		switch {
		case status == http.StatusUnauthorized:
			reason = "unauthenticated"
		case status == http.StatusForbidden:
			reason = "forbidden"
		case status == http.StatusNotFound && c.GetBool(scopeDeniedKey):
			reason = "out_of_scope"
		default:
			return
		}

		actor := "anonymous"
		if cfg.Actor != nil {
			actor = cfg.Actor(c)
		}
		ip := c.ClientIP()
		key := actor
		if actor == "anonymous" {
			key = "ip:" + ip
		}
		ok, suppressed := admit(key)
		if !ok {
			return
		}

		permission := c.GetString(permissionKey)
		if permission == "" && cfg.Permission != nil {
			permission = cfg.Permission(c)
		}
		route := c.FullPath()
		if route == "" {
			route = "(unmatched)"
		}
		attrs := []any{
			"reason", reason, "status", status, "actor", actor, "permission", permission,
			"method", c.Request.Method, "route", route, "ip", ip, "request_id", RequestIDFrom(c),
		}
		if suppressed > 0 {
			attrs = append(attrs, "suppressed_before", suppressed)
		}
		cfg.Logger.Warn("request denied", attrs...)
	}
}
