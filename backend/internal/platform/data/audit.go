package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
)

// Audit actor types (system-design.txt A.1, A.8, build-phases.txt 3f-4).
// The manager's own login reads as "manager", never blank.
const (
	ActorManager = "manager"
	ActorStaff   = "staff"
	ActorAdmin   = "admin"
	ActorSystem  = "system"
)

// AuditEntry is one row a mutation writes to audit_log.
type AuditEntry struct {
	// ActorType identifies who performed the action. ActorID is that
	// principal's id — required unless ActorType is ActorSystem (a
	// scheduled job, nobody signed in).
	ActorType string
	ActorID   *uuid.UUID
	// ActorName is the job's name for a system actor (C3.4), e.g.
	// "usage-recount". Required for ActorSystem and ignored otherwise.
	ActorName string
	// Product is "" for a platform-only action (e.g. staff invited) —
	// stored as NULL.
	Product    string
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	Summary    string
	// IP is the client address as the API resolved it (c.ClientIP(): the TCP
	// peer, or X-Forwarded-For only from a trusted proxy). Empty for a system
	// action; stored as NULL.
	IP string
}

// AuditModel is tenant-scoped, append-only audit_log access.
type AuditModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Log writes one audit_log row for tenantID. Failures are the caller's to
// handle — logging an audit failure must never look like the mutation
// itself failed, but it must also never be silently swallowed (callers
// log it, count it — see the Super Admin health page's audit_failures_24h
// — and continue, the same way usage-reporting failures do).
func (m AuditModel) Log(ctx context.Context, tenantID uuid.UUID, e AuditEntry) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var product *string
	if e.Product != "" {
		product = &e.Product
	}
	var ip *string
	if e.IP != "" {
		ip = &e.IP
	}
	// staff_id is kept alongside actor_type/actor_id (migration 000018)
	// only when the actor actually is a staff member, for joins.
	var staffID *uuid.UUID
	if e.ActorType == ActorStaff {
		staffID = e.ActorID
	}

	var actorName *string
	if e.ActorType == ActorSystem {
		if e.ActorName == "" {
			return errors.New("audit: a system actor must name its job")
		}
		actorName = &e.ActorName
	}

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		return q.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ManagerID: tenantID, StaffID: staffID, ActorType: e.ActorType, ActorID: e.ActorID, ActorName: actorName,
			Product: product, Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID, Summary: e.Summary, Ip: ip,
		})
	})
}
