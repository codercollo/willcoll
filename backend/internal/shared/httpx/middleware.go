package httpx

import (
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RecoverPanic converts a panic in a later handler into a Greenlight-style
// JSON 500 instead of Gin's plain-text abort, and sets Connection: close so
// the server drops a connection whose state is now unknown. serverError is
// the product's 500 responder (e.g. Responder.ServerErrorResponse).
func RecoverPanic(serverError func(c *gin.Context, err error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				c.Writer.Header().Set("Connection", "close")

				err, ok := r.(error)
				if !ok {
					err = fmt.Errorf("%v", r)
				}

				serverError(c, err)
				c.Abort()
			}
		}()

		c.Next()
	}
}

// LogRequest writes one structured log line per request.
func LogRequest(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		logger.Info("request",
			"method", c.Request.Method,
			"uri", RedactQueryToken(c.Request.URL),
			"status", c.Writer.Status(),
			"bytes", c.Writer.Size(),
			"duration", time.Since(start).String(),
			"ip", c.ClientIP(),
			"request_id", RequestIDFrom(c),
		)
	}
}

// RedactQueryToken returns u's request URI with every "token" query value
// replaced by REDACTED. A webhook callback URL that carries a shared secret
// as ?token=... must never land in a log line verbatim.
func RedactQueryToken(u *url.URL) string {
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

// Request metrics, published on /debug/vars by expvar. Declared once at
// package level: expvar names must be unique for the life of the process.
var (
	totalRequestsReceived           = expvar.NewInt("requests_received_total")
	totalResponsesSent              = expvar.NewInt("responses_sent_total")
	totalProcessingTimeMicroseconds = expvar.NewInt("processing_time_microseconds")
	totalResponsesSentByStatus      = expvar.NewMap("responses_sent_by_status")
)

// Metrics counts requests, responses, processing time and response status
// codes.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		totalRequestsReceived.Add(1)

		c.Next()

		totalResponsesSent.Add(1)
		totalProcessingTimeMicroseconds.Add(time.Since(start).Microseconds())
		totalResponsesSentByStatus.Add(strconv.Itoa(c.Writer.Status()), 1)
	}
}

// CORS lets a trusted origin call the API with credentials: 'include', and
// answers CORS preflight requests directly. Untrusted origins get no CORS
// headers, so the browser blocks them.
func CORS(trustedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Add("Vary", "Origin")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")

		origin := c.GetHeader("Origin")
		if origin != "" && slices.Contains(trustedOrigins, origin) {
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

// RateLimiterConfig configures RateLimit.
type RateLimiterConfig struct {
	Enabled bool
	RPS     float64
	Burst   int
}

// rateLimitClient tracks one IP's token-bucket limiter and when it was last
// seen, so the cleanup goroutine can evict clients that have gone quiet.
type rateLimitClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimit is per-IP token-bucket rate limiting. The IP comes from
// c.ClientIP(), which only honours X-Forwarded-For from trusted proxies
// (configured at the router level); otherwise any caller could dodge the
// limit by spoofing the header. Build it once and share the handler
// between route groups so they draw on the same buckets. exceeded is the
// product's 429 responder.
func RateLimit(cfg RateLimiterConfig, exceeded func(c *gin.Context)) gin.HandlerFunc {
	return RateLimitBy(cfg, func(c *gin.Context) string { return c.ClientIP() }, exceeded)
}

// RateLimitBy is RateLimit with a caller-chosen bucket key instead of the
// client IP: use it where many legitimate clients share one IP (carrier-grade
// NAT) and the resource being protected has a better identity, such as an
// intent id. An empty key shares one bucket per limiter, so key functions
// should fall back to something non-empty (the IP).
func RateLimitBy(cfg RateLimiterConfig, key func(c *gin.Context) string, exceeded func(c *gin.Context)) gin.HandlerFunc {
	var (
		mu      sync.Mutex
		clients = make(map[string]*rateLimitClient)
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
		if !cfg.Enabled {
			c.Next()
			return
		}

		ip := key(c)

		mu.Lock()
		cl, found := clients[ip]
		if !found {
			cl = &rateLimitClient{limiter: rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst)}
			clients[ip] = cl
		}
		cl.lastSeen = time.Now()
		allowed := cl.limiter.Allow()
		mu.Unlock()

		if !allowed {
			exceeded(c)
			c.Abort()
			return
		}

		c.Next()
	}
}
