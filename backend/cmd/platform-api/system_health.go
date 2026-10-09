package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/shared/dbhealth"
	"github.com/gin-gonic/gin"
)

// adminSystemHealthHandler handles GET /v1/admin/system/health: the Super
// Admin's one health page for the whole platform. It checks each service
// independently and in parallel, every check under its own short timeout
// (HEALTH_TIMEOUT), and returns whatever it got, so one dead or slow
// dependency shows up as that service being "unreachable" instead of taking
// the page down:
//
//   - platform: willcoll_platform, read directly (latency, size, connections,
//     largest tables, WAL archiver) plus this binary's audit-failure count.
//   - rentals: asked over HTTP for its own report (its own database, backups,
//     usage reporting, audit failures, inbox fallbacks). platform-api never
//     opens a product database. The admin's own session cookie is forwarded:
//     rentals-api already authenticates a Super Admin from the shared
//     platform session, so no second credential exists.
//
// Status: "available" when every service is ok, "degraded" when any other
// service is unhealthy or unreachable (still HTTP 200, with the partial
// results), "unavailable" with HTTP 503 only when the platform database
// itself is unreachable, so an uptime check can alert on the status code.
func (app *application) adminSystemHealthHandler(c *gin.Context) {
	timeout := app.config.healthTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	var platform, rentals envelope
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		platform = app.checkPlatformService(c.Request.Context(), timeout)
	}()
	go func() {
		defer wg.Done()
		rentals = checkProductService(c.Request.Context(), timeout, app.config.productAPIs.rentals, c.GetHeader("Cookie"))
	}()
	wg.Wait()

	status, code := "available", http.StatusOK
	switch {
	case platform["status"] != "ok":
		status, code = "unavailable", http.StatusServiceUnavailable
	case rentals["status"] != "ok" && rentals["status"] != "not_configured":
		status = "degraded"
	}

	body := envelope{
		"status":     status,
		"checked_at": time.Now().UTC(),
		"system_info": envelope{
			"environment": app.config.env,
			"version":     buildVersion(),
		},
		"services": envelope{"platform": platform, "rentals": rentals},
	}
	if err := app.writeJSON(c, code, body, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// checkPlatformService reads willcoll_platform on the admin pool, which is
// the one pool the page can rely on (BYPASSRLS, reserved for admin traffic).
func (app *application) checkPlatformService(parent context.Context, timeout time.Duration) envelope {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
	h, err := dbhealth.Check(ctx, app.adminDB)
	latency := time.Since(start)
	if err != nil {
		app.logger.Error("system health: platform database check failed", "error", err)
		return envelope{"status": "unreachable", "database": envelope{"status": "unreachable", "error": checkError(err)}}
	}
	return envelope{
		"status": "ok",
		"database": envelope{
			"status":         "ok",
			"latency_ms":     latency.Milliseconds(),
			"size_bytes":     h.SizeBytes,
			"connections":    h.Connections,
			"largest_tables": h.LargestTables,
		},
		"wal_archiving": envelope{
			"last_successful_at": h.LastArchivedTime,
			"last_archived_wal":  h.LastArchivedWAL,
			"archived_count":     h.WALArchivedCount,
			"failed_count":       h.WALFailedCount,
			"last_failed_at":     h.LastFailedTime,
		},
		"audit_failures_24h": app.auditFailures.count24h(time.Now()),
		"email_outbox":       app.emailOutboxStatus(ctx),
		// Received money nobody has claimed: open rows need a Super Admin.
		"unclaimed_receipts": app.unclaimedReceiptsStatus(ctx),
	}
}

// healthClient is shared so repeated page loads reuse a connection. Its own
// Timeout is a backstop; each call is also bound by a context deadline.
var healthClient = &http.Client{Timeout: 10 * time.Second}

// checkProductService asks one product API for its own admin health report.
// It never returns an error: every failure becomes a status the page can show.
//
//	ok              the product answered 200 and reports itself available
//	degraded        it answered, but reports a problem (e.g. stale backups)
//	unreachable     no answer in time, an error status, or its database is down
//	unauthorized    it refused the admin's session (a cookie or deploy mismatch)
//	not_configured  no URL is set for this product
func checkProductService(parent context.Context, timeout time.Duration, baseURL, cookie string) envelope {
	if baseURL == "" {
		return envelope{"status": "not_configured"}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/admin/system/health", nil)
	if err != nil {
		return envelope{"status": "unreachable", "error": "bad product API URL"}
	}
	req.Header.Set("Accept", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	start := time.Now()
	resp, err := healthClient.Do(req)
	if err != nil {
		return envelope{"status": "unreachable", "error": checkError(err), "latency_ms": time.Since(start).Milliseconds()}
	}
	defer func() { _ = resp.Body.Close() }()
	latency := time.Since(start).Milliseconds()

	var report map[string]any
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &report)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return envelope{"status": "unauthorized", "latency_ms": latency, "error": "the product API refused the admin session"}
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable:
		return envelope{"status": "unreachable", "latency_ms": latency, "error": http.StatusText(resp.StatusCode)}
	}

	// A 503 from rentals-api carries its own "unavailable" report: its database
	// is down, so there is nothing to show beyond that.
	status := "ok"
	switch s, _ := report["status"].(string); {
	case resp.StatusCode == http.StatusServiceUnavailable || s == "unavailable":
		status = "unreachable"
	case s != "available":
		status = "degraded"
	}
	return envelope{"status": status, "latency_ms": latency, "report": report}
}

// checkError is a client-safe reason for a failed check: never a DSN or a
// driver's connection string.
func checkError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "connection failed"
	}
}

// emailOutboxStatus is the owner-alert queue as the System page shows it
// (C3.7): what is waiting, what ran out of retries, how long the oldest has
// waited and why the latest failure failed, plus alerts that could not even be
// queued. A dead alert is never silent.
func (app *application) emailOutboxStatus(ctx context.Context) envelope {
	status := envelope{"alert_enqueue_failures_24h": app.alertFailures.count24h(time.Now())}
	stats, err := app.models.Outbox.Stats(ctx)
	if err != nil {
		app.logger.Error("system health: reading the email outbox", "error", err)
		status["status"] = "unreachable"
		return status
	}
	status["status"] = "ok"
	status["pending"] = stats.Pending
	status["dead"] = stats.Dead
	status["sent_24h"] = stats.Sent24h
	status["oldest_pending_age_seconds"] = stats.OldestPendingAgeSeconds
	status["last_error"] = stats.LastError
	return status
}
