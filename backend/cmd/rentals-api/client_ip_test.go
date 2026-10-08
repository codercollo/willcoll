package main

import (
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
)

// httptest.NewRequest gives every request the TCP peer 192.0.2.1, standing in
// for the Nuxt server that sits between Caddy and this API.
const testProxyPeer = "192.0.2.1"

// With the peer in TRUSTED_PROXIES, the rate limiter keys on the real client
// from X-Forwarded-For: two clients behind the proxy no longer share a bucket,
// and a spoofed leftmost entry is not mistaken for the client. (The untrusted
// case, where the header is ignored, is TestRateLimitIgnoresSpoofedForwardedFor.)
func TestRateLimitUsesForwardedForFromTrustedProxy(t *testing.T) {
	m := newMiddlewareTestApp(t)
	m.app.config.limiter.enabled = true
	m.app.config.limiter.rps = 0.001
	m.app.config.limiter.burst = 1
	m.app.config.trustedProxies = []string{testProxyPeer + "/32"}
	router, err := m.app.routes()
	if err != nil {
		t.Fatal(err)
	}
	m.router = router

	get := func(xff string) int {
		return m.do(http.MethodGet, "/v1/pay/some-property/1A", nil, map[string]string{"X-Forwarded-For": xff}).Code
	}

	if code := get("203.0.113.1"); code == http.StatusTooManyRequests {
		t.Fatalf("first request from client .1 limited")
	}
	if code := get("203.0.113.2"); code == http.StatusTooManyRequests {
		t.Errorf("client .2 shares client .1's bucket: the real client address is not being used")
	}
	if code := get("203.0.113.1"); code != http.StatusTooManyRequests {
		t.Errorf("client .1 again = %d, want 429", code)
	}

	// A client that prepends a fake address still lands in its own bucket:
	// the proxy appended the address it really saw on the right.
	if code := get("6.6.6.6, 203.0.113.9"); code == http.StatusTooManyRequests {
		t.Fatalf("first request from client .9 limited")
	}
	if code := get("7.7.7.7, 203.0.113.9"); code != http.StatusTooManyRequests {
		t.Errorf("spoofed leftmost entry bought a fresh bucket: got %d, want 429", code)
	}
}

// audit_log records the client address c.ClientIP() resolved: the forwarded
// one behind a trusted proxy, the TCP peer when the header comes from anyone
// else.
func TestAuditRecordsResolvedClientIP(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		xff     string
		want    string
	}{
		{"trusted proxy: the forwarded client", []string{testProxyPeer + "/32"}, "203.0.113.7", "203.0.113.7"},
		{"trusted proxy: spoofed leftmost entry ignored", []string{testProxyPeer + "/32"}, "6.6.6.6, 203.0.113.7", "203.0.113.7"},
		{"untrusted peer: spoofed header ignored", nil, "6.6.6.6", testProxyPeer},
		{"untrusted peer: no header", nil, "", testProxyPeer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)

			var mu sync.Mutex
			var got *string
			m.pq.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, p platformsqlc.CreateAuditLogParams) error {
					mu.Lock()
					defer mu.Unlock()
					got = p.Ip
					return nil
				})

			engine := gin.New()
			if err := engine.SetTrustedProxies(tc.trusted); err != nil {
				t.Fatal(err)
			}
			engine.POST("/audited", func(c *gin.Context) {
				contextSetTenantID(c, uuid.New())
				m.app.audit(c, "test.action", "test", nil, "test")
				c.Status(http.StatusNoContent)
			})
			m.router = engine

			headers := map[string]string{}
			if tc.xff != "" {
				headers["X-Forwarded-For"] = tc.xff
			}
			if w := m.do(http.MethodPost, "/audited", nil, headers); w.Code != http.StatusNoContent {
				t.Fatalf("status = %d", w.Code)
			}
			m.app.wg.Wait()

			mu.Lock()
			defer mu.Unlock()
			if got == nil || *got != tc.want {
				t.Errorf("audit_log ip = %v, want %q", deref(got), tc.want)
			}
		})
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
