package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
)

// AdminAuditEntry is one Super Admin action: who did it, from where, to what, and
// the reason or note they gave. Summary never carries a password, a token or a
// raw request body.
type AdminAuditEntry struct {
	AdminID uuid.UUID
	// ManagerID is the firm the action concerned; nil for a platform-wide one
	// (the plan catalog, an admin step-up).
	ManagerID  *uuid.UUID
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	Summary    string
	IP         string
}

// AdminAuditRow is one stored row, as read back.
type AdminAuditRow struct {
	ID         uuid.UUID
	AdminID    *uuid.UUID
	ManagerID  *uuid.UUID
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	Summary    string
	IP         string
	CreatedAt  time.Time
}

// AdminAuditModel is the Super Admin's append-only trail (admin_audit_log,
// migration 000026). It has no Update or Delete.
type AdminAuditModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Log appends one row.
func (m AdminAuditModel) Log(ctx context.Context, e AdminAuditEntry) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if e.AdminID == uuid.Nil || e.Action == "" {
		return errors.New("admin audit: an entry needs an admin and an action")
	}
	var ip *string
	if e.IP != "" {
		ip = &e.IP
	}
	admin := e.AdminID
	return m.Store.CreateAdminAuditLog(ctx, sqlc.CreateAdminAuditLogParams{
		AdminID: &admin, ManagerID: e.ManagerID, Action: e.Action, EntityType: e.EntityType,
		EntityID: e.EntityID, Summary: e.Summary, Ip: ip,
	})
}

// Recent returns the newest rows first.
func (m AdminAuditModel) Recent(ctx context.Context, limit int) ([]AdminAuditRow, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.ListAdminAuditLog(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]AdminAuditRow, len(rows))
	for i, r := range rows {
		out[i] = AdminAuditRow{ID: r.ID, AdminID: r.AdminID, ManagerID: r.ManagerID, Action: r.Action, EntityType: r.EntityType,
			EntityID: r.EntityID, Summary: r.Summary, CreatedAt: r.CreatedAt}
		if r.Ip != nil {
			out[i].IP = *r.Ip
		}
	}
	return out, nil
}
