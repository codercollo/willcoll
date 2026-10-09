package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func denialRouter(logs *bytes.Buffer, now func() time.Time, perActor int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), DenialLog(DenialConfig{
		Logger:            slog.New(slog.NewTextHandler(logs, nil)),
		PerActorPerMinute: perActor,
		Now:               now,
		Actor: func(c *gin.Context) string {
			if who := c.GetHeader("X-Test-Actor"); who != "" {
				return who
			}
			return "anonymous"
		},
		Permission: func(c *gin.Context) string { return "units.write" },
	}))
	r.GET("/units/:id", func(c *gin.Context) {
		switch c.Query("as") {
		case "401":
			c.Status(http.StatusUnauthorized)
		case "403":
			NoteDeniedPermission(c, "payment_channels.manage")
			c.Status(http.StatusForbidden)
		case "scope":
			MarkScopeDenied(c)
			c.Status(http.StatusNotFound)
		case "404":
			c.Status(http.StatusNotFound)
		default:
			c.Status(http.StatusOK)
		}
	})
	return r
}

func serve(r *gin.Engine, target, actor string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.RemoteAddr = "203.0.113.9:4000"
	req.Header.Set("Cookie", "willcoll_session=SECRETSESSION")
	if actor != "" {
		req.Header.Set("X-Test-Actor", actor)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDenialLogRecordsActorPermissionRouteIPAndRequestID(t *testing.T) {
	var logs bytes.Buffer
	r := denialRouter(&logs, time.Now, 0)

	w := serve(r, "/units/abc?as=403&token=SECRETTOKEN", "staff:s1")
	id := w.Header().Get(RequestIDHeader)
	if id == "" {
		t.Fatal("no X-Request-Id on the response")
	}
	line := logs.String()
	for _, want := range []string{
		"request denied", "reason=forbidden", "status=403", "actor=staff:s1",
		"permission=payment_channels.manage", "method=GET", "route=/units/:id", "ip=203.0.113.9", "request_id=" + id,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line lacks %q: %s", want, line)
		}
	}
	// Never the cookie, the query string or the raw path.
	for _, leak := range []string{"SECRETSESSION", "SECRETTOKEN", "/units/abc"} {
		if strings.Contains(line, leak) {
			t.Errorf("log line leaks %q: %s", leak, line)
		}
	}
}

func TestDenialLogCoversExactlyTheDenials(t *testing.T) {
	for as, want := range map[string]string{
		"401":   "reason=unauthenticated",
		"403":   "reason=forbidden",
		"scope": "reason=out_of_scope",
		"404":   "", // an ordinary not-found is not a denial
		"ok":    "",
	} {
		var logs bytes.Buffer
		serve(denialRouter(&logs, time.Now, 0), "/units/abc?as="+as, "")
		got := logs.String()
		if want == "" && got != "" {
			t.Errorf("%s logged a denial: %s", as, got)
		}
		if want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: want %q in %q", as, want, got)
		}
	}
	// The permission comes from the route table when none was noted.
	var logs bytes.Buffer
	serve(denialRouter(&logs, time.Now, 0), "/units/abc?as=401", "")
	if !strings.Contains(logs.String(), "permission=units.write") {
		t.Errorf("route-table permission missing: %s", logs.String())
	}
}

func TestDenialLogIsRateLimitedPerActor(t *testing.T) {
	var logs bytes.Buffer
	clock := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	r := denialRouter(&logs, func() time.Time { return clock }, 3)

	for range 50 {
		serve(r, "/units/abc?as=403", "staff:flooder")
	}
	if n := strings.Count(logs.String(), "actor=staff:flooder"); n != 3 {
		t.Fatalf("flooder produced %d lines in one window, want 3", n)
	}

	// Another actor is unaffected by the flooder's budget.
	serve(r, "/units/abc?as=403", "staff:other")
	if !strings.Contains(logs.String(), "actor=staff:other") {
		t.Error("a second actor was suppressed by the first one's flood")
	}

	// Next window: logging resumes and says how many were dropped.
	clock = clock.Add(61 * time.Second)
	logs.Reset()
	serve(r, "/units/abc?as=403", "staff:flooder")
	if !strings.Contains(logs.String(), "suppressed_before=47") {
		t.Errorf("first line of the next window does not report the 47 suppressed: %s", logs.String())
	}
}

func TestDenialLogLimitsAnonymousCallersPerIP(t *testing.T) {
	var logs bytes.Buffer
	r := denialRouter(&logs, time.Now, 2)
	for range 20 {
		serve(r, "/units/abc?as=401", "")
	}
	if n := strings.Count(logs.String(), "actor=anonymous"); n != 2 {
		t.Errorf("anonymous scanner from one IP produced %d lines, want 2", n)
	}
}

func TestRequestIDIsFreshAndNeverTakenFromTheClient(t *testing.T) {
	var logs bytes.Buffer
	r := denialRouter(&logs, time.Now, 0)
	req := httptest.NewRequest("GET", "/units/abc", nil)
	req.Header.Set(RequestIDHeader, "client-chosen")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get(RequestIDHeader); got == "" || got == "client-chosen" {
		t.Errorf("request id = %q", got)
	}
}
