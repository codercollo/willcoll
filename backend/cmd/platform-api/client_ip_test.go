package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// httptest.NewRequest gives every request the TCP peer 192.0.2.1, standing in
// for the Nuxt server between Caddy and this API.
const testProxyPeer = "192.0.2.1"

// audit_log records the client address c.ClientIP() resolved: the forwarded
// one behind a trusted proxy, the TCP peer when the header comes from anyone
// else, so a spoofed X-Forwarded-For is never recorded as the actor's IP.
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
			f := newFakePlatform(t)

			engine := gin.New()
			if err := engine.SetTrustedProxies(tc.trusted); err != nil {
				t.Fatal(err)
			}
			engine.POST("/audited", func(c *gin.Context) {
				contextSetTenantID(c, fakeManagerID)
				f.app.audit(c, "", "test.action", "test", &uuid.Nil, "test")
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, "/audited", nil)
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d", w.Code)
			}
			f.app.wg.Wait()

			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.audit) != 1 {
				t.Fatalf("audit rows = %d, want 1", len(f.audit))
			}
			if ip := f.audit[0].Ip; ip == nil || *ip != tc.want {
				t.Errorf("audit_log ip = %v, want %q", ip, tc.want)
			}
		})
	}
}
