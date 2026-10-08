package main

import (
	"context"
	"sync"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// audit writes one audit_log row for the signed-in tenant, in the
// background (build-phases.txt 3f-4) — additive, never load-bearing for
// the request that triggered it, same pattern as the usage reporter
// (usage.go). A nil app.platform (no PLATFORM_DB_DSN, development only)
// makes this a deliberate no-op, not an error.
//
// action/entityType/summary must never carry a request body, a phone
// number or other personal data — a short, human-readable line like
// "water run 2026-09, 42 units, KES 63,000".
func (app *application) audit(c *gin.Context, action, entityType string, entityID *uuid.UUID, summary string) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		return
	}
	actorType, actorID := platformdata.ActorManager, tenantID
	if staff := contextGetStaff(c); !staff.IsAnonymous() {
		actorType, actorID = platformdata.ActorStaff, staff.ID
	}
	app.writeAudit(tenantID, platformdata.AuditEntry{
		ActorType: actorType, ActorID: &actorID, Product: platformdata.ProductRentals,
		Action: action, EntityType: entityType, EntityID: entityID, Summary: summary, IP: c.ClientIP(),
	})
}

// writeAudit is what audit shares once they've worked out the
// actor: fire-and-forget in the background, nil app.platform is a no-op,
// and a failure is logged and counted (see auditFailures /
// usageReportingStatus's sibling on the Super Admin health page) rather
// than silently dropped.
func (app *application) writeAudit(tenantID uuid.UUID, entry platformdata.AuditEntry) {
	if app.platform == nil {
		return
	}
	app.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.platform.Audit.Log(ctx, tenantID, entry); err != nil {
			app.logger.Error("audit log write failed", "manager_id", tenantID, "action", entry.Action, "entity_type", entry.EntityType, "error", err)
			app.auditFailures.record(time.Now())
		}
	})
}

// auditFailureTracker counts audit_log write failures in a trailing
// window, for the Super Admin health page's "audit_failures_24h" — a
// fire-and-forget write can otherwise fail silently forever.
type auditFailureTracker struct {
	mu    sync.Mutex
	times []time.Time
}

func (t *auditFailureTracker) record(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.times = append(t.times, at)
	t.prune(at)
}

// count24h returns how many failures were recorded in the 24 hours up to
// now, pruning anything older in the same pass.
func (t *auditFailureTracker) count24h(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(now)
	return len(t.times)
}

// prune drops entries older than 24 hours before at. Caller holds the lock.
func (t *auditFailureTracker) prune(at time.Time) {
	cutoff := at.Add(-24 * time.Hour)
	i := 0
	for i < len(t.times) && t.times[i].Before(cutoff) {
		i++
	}
	t.times = t.times[i:]
}
