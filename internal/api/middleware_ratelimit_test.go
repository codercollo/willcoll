package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/google/uuid"
)

// TestTokenBucketAllowsBurstThenBlocks proves the bucket grants exactly its
// capacity before denying, with no refill in play (zero elapsed time).
func TestTokenBucketAllowsBurstThenBlocks(t *testing.T) {
	b := newTokenBucket(3, 0)

	for i := 0; i < 3; i++ {
		if !b.allow() {
			t.Fatalf("request %d: expected allow within burst capacity", i)
		}
	}

	if b.allow() {
		t.Fatal("expected the 4th request to be denied once the burst is exhausted")
	}
}

// TestRateLimitByIPScopesBucketsPerKey proves one IP's exhausted allowance
// doesn't affect another IP.
func TestRateLimitByIPScopesBucketsPerKey(t *testing.T) {
	s := &Server{authIPLimiter: newRateLimiter(1, 0)}
	handler := s.rateLimitByIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodPost, "http://example/v1/auth/login", nil)
	first.RemoteAddr = "203.0.113.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, first)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request from 203.0.113.1: status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Same IP again: burst of 1 is spent, so this must be throttled.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, first)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from 203.0.113.1: status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}

	// A different IP has its own bucket and isn't affected.
	second := httptest.NewRequest(http.MethodPost, "http://example/v1/auth/login", nil)
	second.RemoteAddr = "203.0.113.2:5678"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, second)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request from 203.0.113.2: status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// TestRateLimitByUserScopesBucketsPerUser proves one user's exhausted
// allowance doesn't affect another user, and that the middleware requires
// authenticated claims to already be on the request context.
func TestRateLimitByUserScopesBucketsPerUser(t *testing.T) {
	s := &Server{paymentsUserLimiter: newRateLimiter(1, 0)}
	handler := s.rateLimitByUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	withClaims := func(userID uuid.UUID) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "http://example/v1/payments", nil)
		claims := auth.Claims{UserID: userID, Role: "manager", OrganizationID: uuid.New()}
		return req.WithContext(context.WithValue(req.Context(), claimsContextKey, claims))
	}

	userA := uuid.New()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, withClaims(userA))
	if rr.Code != http.StatusOK {
		t.Fatalf("first payment for user A: status = %d, want %d", rr.Code, http.StatusOK)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, withClaims(userA))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second payment for user A: status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}

	userB := uuid.New()
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, withClaims(userB))
	if rr.Code != http.StatusOK {
		t.Fatalf("first payment for user B: status = %d, want %d", rr.Code, http.StatusOK)
	}

	// No claims on the request at all: rejected before the limiter is consulted.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "http://example/v1/payments", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}
