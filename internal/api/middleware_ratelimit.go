package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// tokenBucket is a hand-rolled token-bucket limiter for a single key (one IP
// or one user). It starts full, refills continuously at refillPerSec, and
// denies once it runs dry (LGF §11).
type tokenBucket struct {
	mu           sync.Mutex
	tokens       float64
	capacity     float64
	refillPerSec float64
	lastRefill   time.Time
	lastSeen     time.Time
}

func newTokenBucket(capacity, refillPerSec float64) *tokenBucket {
	now := time.Now()
	return &tokenBucket{
		tokens:       capacity,
		capacity:     capacity,
		refillPerSec: refillPerSec,
		lastRefill:   now,
		lastSeen:     now,
	}
}

// allow refills the bucket for the elapsed time, then takes one token if
// available.
func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	b.tokens += now.Sub(b.lastRefill).Seconds() * b.refillPerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (b *tokenBucket) idleSince(cutoff time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastSeen.Before(cutoff)
}

// rateLimiter hands out one tokenBucket per key, so distinct IPs/users each
// get their own independent allowance. A background goroutine evicts buckets
// that have gone quiet, so the map doesn't grow without bound under a flood
// of distinct keys.
type rateLimiter struct {
	mu           sync.Mutex
	buckets      map[string]*tokenBucket
	capacity     float64
	refillPerSec float64
}

func newRateLimiter(capacity, refillPerSec float64) *rateLimiter {
	rl := &rateLimiter{
		buckets:      make(map[string]*tokenBucket),
		capacity:     capacity,
		refillPerSec: refillPerSec,
	}
	go rl.evictIdle()
	return rl
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	b, ok := rl.buckets[key]
	if !ok {
		b = newTokenBucket(rl.capacity, rl.refillPerSec)
		rl.buckets[key] = b
	}
	rl.mu.Unlock()

	return b.allow()
}

func (rl *rateLimiter) evictIdle() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cutoff := time.Now().Add(-3 * time.Minute)

		rl.mu.Lock()
		for key, b := range rl.buckets {
			if b.idleSince(cutoff) {
				delete(rl.buckets, key)
			}
		}
		rl.mu.Unlock()
	}
}

// rateLimitByIP throttles unauthenticated /v1/auth/* endpoints per client IP
// (spec §6.2): a burst of 10 then a steady 2/sec, enough headroom for normal
// login/refresh retries without allowing a tight brute-force loop.
func (s *Server) rateLimitByIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authIPLimiter.allow(clientIP(r)) {
			writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded, please try again shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitByUser throttles POST /v1/payments per authenticated user (spec
// §6.2): a burst of 3 then one every two seconds, enough to blunt an
// accidental double-submit (e.g. a double-clicked "record payment" button)
// without getting in the way of normal use. Must run after authenticate, since
// it reads the caller's identity from the request's verified claims.
func (s *Server) rateLimitByUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
			return
		}

		if !s.paymentsUserLimiter.allow(claims.UserID.String()) {
			writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded, please try again shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the caller's address without its port, falling back to the
// raw RemoteAddr if it isn't in host:port form.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
