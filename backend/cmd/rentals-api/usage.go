package main

import (
	"context"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/google/uuid"
)

// usageStatusTracker records the usage reporter's last successful write,
// for the Super Admin health page. Safe for concurrent use: reportUsage
// runs in a background goroutine per call.
type usageStatusTracker struct {
	mu             sync.Mutex
	lastReportedAt time.Time
}

func (t *usageStatusTracker) recordSuccess(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if at.After(t.lastReportedAt) {
		t.lastReportedAt = at
	}
}

// LastReportedAt returns the last successful report time, or the zero
// value if none has happened yet this process (a fresh restart, or usage
// reporting disabled).
func (t *usageStatusTracker) LastReportedAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastReportedAt
}

// usageReportingStatus is the Super Admin health page's view of the usage
// reporter (build-phases.txt 3e step 2): whether it's enabled at all
// (PLATFORM_DB_DSN set and connected), and when it last wrote successfully
// — null if it never has (the startup recount normally sets it within
// moments of a restart, unless there are no managers or the write failed).
func (app *application) usageReportingStatus() envelope {
	if app.platform == nil {
		return envelope{"enabled": false, "last_report_at": nil}
	}
	last := app.usageStatus.LastReportedAt()
	if last.IsZero() {
		return envelope{"enabled": true, "last_report_at": nil}
	}
	return envelope{"enabled": true, "last_report_at": last}
}

// reportUsage recomputes tenantID's active-unit count and reports it to
// willcoll_platform's module_usage (build-phases.txt 3e; system-design.txt
// A.7). Called on a unit create, a property archive/restore, and a bulk
// units import — the three ways the count changes outside the daily
// re-count sweep.
//
// Always runs in the background (app.background): this is additive
// telemetry for billing, never load-bearing for the request that
// triggered it. A slow or failing platform connection must not slow down
// or fail a unit-creation request. A nil app.platform (no PLATFORM_DB_DSN)
// makes this a deliberate no-op, not an error.
func (app *application) reportUsage(tenantID uuid.UUID) {
	if app.platform == nil {
		return
	}
	app.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		count, err := app.models.Units.CountForManager(ctx, tenantID)
		if err != nil {
			app.logger.Error("usage reporter: counting units", "tenant_id", tenantID, "error", err)
			return
		}
		if _, err := app.platform.Usage.Upsert(ctx, tenantID, data.ProductRentals, count); err != nil {
			app.logger.Error("usage reporter: upserting module_usage", "tenant_id", tenantID, "error", err)
			return
		}
		app.usageStatus.recordSuccess(time.Now())
	})
}

// startUsageRecountJob runs reportUsage for every manager once a day, as a
// safety net against a missed upsert (a bug, a crash mid-request, a
// manual database edit). Stops when ctx is cancelled (server.go's
// shutdown). A no-op loop, never started, when usage reporting is
// disabled.
func (app *application) startUsageRecountJob(ctx context.Context) {
	if app.platform == nil {
		return
	}

	go func() {
		// Report once at startup too, so the health page's last_report_at is
		// accurate right after a restart instead of "no report yet" until a
		// unit changes or the first daily tick.
		app.recountAllManagers(ctx)

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.recountAllManagers(ctx)
			}
		}
	}()
}

// recountAllManagers reports every manager's current unit count. Errors
// are logged per-manager and never stop the sweep — one manager's bad
// data must not silently skip everyone after them.
func (app *application) recountAllManagers(ctx context.Context) {
	ids, err := app.models.Platform.ListTenantIDs(ctx)
	if err != nil {
		app.logger.Error("usage reporter: listing managers for daily recount", "error", err)
		return
	}
	for _, id := range ids {
		app.reportUsage(id)
	}
}
