package main

import (
	"context"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// audit writes one audit_log row for the signed-in tenant, in the
// background (build-phases.txt 3f-4) — additive, never load-bearing for
// the request that triggered it. actor_type is "staff" or "manager" (the
// manager's own login is never blank); use auditAdmin for a Super Admin
// action on a specific manager's data.
//
// action/entityType/summary must never carry a request body, a phone
// number or other personal data.
func (app *application) audit(c *gin.Context, product, action, entityType string, entityID *uuid.UUID, summary string) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		return
	}
	actorType, actorID := data.ActorManager, tenantID
	if staff := contextGetStaff(c); !staff.IsAnonymous() {
		actorType, actorID = data.ActorStaff, staff.ID
	}
	app.writeAudit(tenantID, data.AuditEntry{
		ActorType: actorType, ActorID: &actorID, Product: product,
		Action: action, EntityType: entityType, EntityID: entityID, Summary: summary, IP: c.ClientIP(),
	})
}

// auditAdmin writes one audit_log row for a Super Admin action on
// targetTenantID's data — the signed-in principal is the admin, not a
// tenant, so the tenant to scope the row to must be given explicitly.
func (app *application) auditAdmin(c *gin.Context, targetTenantID uuid.UUID, product, action, entityType string, entityID *uuid.UUID, summary string) {
	admin, ok := contextGetAdmin(c)
	if !ok {
		return
	}
	app.writeAudit(targetTenantID, data.AuditEntry{
		ActorType: data.ActorAdmin, ActorID: &admin.ID, Product: product,
		Action: action, EntityType: entityType, EntityID: entityID, Summary: summary, IP: c.ClientIP(),
	})
}

// auditAdminAction appends one row to the Super Admin's own trail
// (admin_audit_log, migration 000026): who (the signed-in admin), from where (the
// client IP), the target, and a reason or note in summary. Unlike auditAdmin it
// needs no firm, so it also records actions on the global plan catalog. It does
// nothing for a request that is not a Super Admin's. targetManager is the firm
// the action concerned, or nil.
//
// summary must never carry a password, a token or a request body.
func (app *application) auditAdminAction(c *gin.Context, targetManager *uuid.UUID, action, entityType string, entityID *uuid.UUID, summary string) {
	admin, ok := contextGetAdmin(c)
	if !ok {
		return
	}
	entry := data.AdminAuditEntry{
		AdminID: admin.ID, ManagerID: targetManager, Action: action, EntityType: entityType,
		EntityID: entityID, Summary: summary, IP: c.ClientIP(),
	}
	app.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.models.AdminAudit.Log(ctx, entry); err != nil {
			app.logger.Error("admin audit log write failed", "action", entry.Action, "entity_type", entry.EntityType, "error", err)
			app.auditFailures.record(time.Now())
		}
	})
}

func (app *application) writeAudit(tenantID uuid.UUID, entry data.AuditEntry) {
	app.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.models.Audit.Log(ctx, tenantID, entry); err != nil {
			app.logger.Error("audit log write failed", "manager_id", tenantID, "action", entry.Action, "entity_type", entry.EntityType, "error", err)
			app.auditFailures.record(time.Now())
		}
	})
}

// auditFailureTracker counts audit_log write failures in a trailing
// window (build-phases.txt 3f-4). Not yet surfaced on a platform-api
// health page (none exists yet — rentals-api's is the one Super Admin
// health page today); kept here so it's ready once one does.
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

func (t *auditFailureTracker) count24h(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(now)
	return len(t.times)
}

func (t *auditFailureTracker) prune(at time.Time) {
	cutoff := at.Add(-24 * time.Hour)
	i := 0
	for i < len(t.times) && t.times[i].Before(cutoff) {
		i++
	}
	t.times = t.times[i:]
}
