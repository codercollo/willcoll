package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Super Admin endpoints (system-design.txt 1.1, 4.9), all behind
// requireAdmin. Their data comes from app.models.Platform, which runs as
// willcoll_admin (BYPASSRLS, read-mostly — 3.7): the Super Admin sees
// every firm's subscription state and platform health, and never writes a
// manager's tenant data.

// adminListManagersHandler handles GET /v1/admin/managers: every firm with
// its status, latest subscription and portfolio size. Query parameters:
// status (pending|active|suspended), sort (created_at|firm_name, "-" for
// descending; default newest first), page, page_size.
func (app *application) adminListManagersHandler(c *gin.Context) {
	qs := c.Request.URL.Query()
	v := validator.New()

	status := app.readString(qs, "status", "")
	if status != "" {
		v.Check(validator.PermittedValue(status, data.ManagerStatusPending, data.ManagerStatusActive, data.ManagerStatusSuspended),
			"status", "must be pending, active or suspended")
	}

	filters := data.Filters{
		Page:         app.readInt(qs, "page", 1, v),
		PageSize:     app.readInt(qs, "page_size", 20, v),
		Sort:         app.readString(qs, "sort", "-created_at"),
		SortSafelist: []string{"created_at", "-created_at", "firm_name", "-firm_name"},
	}
	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	managers, metadata, err := app.models.Platform.ListManagers(c.Request.Context(), status, filters)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"managers": managers, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminShowManagerHandler handles GET /v1/admin/managers/:id.
func (app *application) adminShowManagerHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	manager, err := app.models.Platform.GetManager(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminSuspendManagerHandler handles POST /v1/admin/managers/:id/suspend.
// The firm is suspended and every session it holds is destroyed on the
// spot: the instant revocation that ADR 0001 chose server-side sessions
// for. (Even without that, requireActivatedManager refuses a suspended
// manager on their next request.) Suspending twice is harmless.
func (app *application) adminSuspendManagerHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	ctx := c.Request.Context()

	if err := app.models.Platform.SuspendManager(ctx, id); err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	revoked, err := app.revokeManagerSessions(ctx, id)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	manager, err := app.models.Platform.GetManager(ctx, id)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	err = app.writeJSON(c, http.StatusOK, envelope{"manager": manager, "sessions_revoked": revoked}, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminReinstateManagerHandler handles POST /v1/admin/managers/:id/reinstate:
// lift a suspension. The firm goes back to active, or to pending if it
// never activated. Reinstating a firm that isn't suspended is harmless.
func (app *application) adminReinstateManagerHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	ctx := c.Request.Context()

	if err := app.models.Platform.ReinstateManager(ctx, id); err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	manager, err := app.models.Platform.GetManager(ctx, id)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// revokeManagerSessions destroys every session signed in as managerID and
// reports how many there were. SCS stores sessions as opaque encoded
// blobs, so it walks them with SessionManager.Iterate rather than
// filtering in SQL; fine for a platform of this size.
func (app *application) revokeManagerSessions(ctx context.Context, managerID uuid.UUID) (int, error) {
	sm := app.sessionManager
	revoked := 0

	err := sm.Iterate(ctx, func(sessionCtx context.Context) error {
		if sm.GetString(sessionCtx, sessionKeyPrincipalType) != principalManager ||
			sm.GetString(sessionCtx, sessionKeyPrincipalID) != managerID.String() {
			return nil
		}
		revoked++
		return sm.Destroy(sessionCtx)
	})
	return revoked, err
}

// adminListSubscriptionsHandler handles GET /v1/admin/subscriptions. Query
// parameters: status (trialing|active|past_due|cancelled), sort
// (current_period_end, "-" for descending; default soonest-ending first),
// page, page_size.
func (app *application) adminListSubscriptionsHandler(c *gin.Context) {
	qs := c.Request.URL.Query()
	v := validator.New()

	status := app.readString(qs, "status", "")
	if status != "" {
		v.Check(validator.PermittedValue(status, data.SubscriptionStatusTrialing, data.SubscriptionStatusActive,
			data.SubscriptionStatusPastDue, data.SubscriptionStatusCancelled),
			"status", "must be trialing, active, past_due or cancelled")
	}

	filters := data.Filters{
		Page:         app.readInt(qs, "page", 1, v),
		PageSize:     app.readInt(qs, "page_size", 20, v),
		Sort:         app.readString(qs, "sort", "current_period_end"),
		SortSafelist: []string{"current_period_end", "-current_period_end"},
	}
	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	subs, metadata, err := app.models.Platform.ListSubscriptions(c.Request.Context(), status, filters)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"subscriptions": subs, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminSystemHealthHandler handles GET /v1/admin/system/health: database
// connectivity, size and largest tables, and the backup indicator
// (system-design.txt 4.9, 8.3). Responds 503 when the database can't be
// reached, so an uptime check can alert on the status code alone.
//
// Disk headroom: the database's own growth is reported here (size, top
// tables). Free space on the database host's disk isn't visible from the
// API container; that belongs to host monitoring (e.g. node-exporter).
func (app *application) adminSystemHealthHandler(c *gin.Context) {
	ctx := c.Request.Context()
	health, latency, err := app.models.Platform.DatabaseHealth(ctx)
	if err != nil {
		app.logError(c, err)
		body := envelope{
			"status":   "unavailable",
			"database": envelope{"status": "unreachable"},
		}
		if werr := app.writeJSON(c, http.StatusServiceUnavailable, body, nil); werr != nil {
			app.serverErrorResponse(c, werr)
		}
		return
	}

	lastBackup, err := app.models.Platform.LastBackup(ctx)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	backups := backupStatus(health, lastBackup, time.Now())

	status := "available"
	if backups["status"] == "failing" || backups["status"] == "stale" {
		status = "degraded"
	}

	body := envelope{
		"status": status,
		"system_info": envelope{
			"environment": app.config.env,
			"version":     buildVersion(),
		},
		"database": envelope{
			"status":         "ok",
			"latency_ms":     latency.Milliseconds(),
			"size_bytes":     health.DatabaseSizeBytes,
			"connections":    health.Connections,
			"largest_tables": health.LargestTables,
		},
		"backups": backups,
	}

	if err := app.writeJSON(c, http.StatusOK, body, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminListBackupsHandler handles GET /v1/admin/backups: the last base
// backup's timestamp and size (recorded by scripts/db_backup_verify.sh),
// and the state of continuous WAL archiving.
func (app *application) adminListBackupsHandler(c *gin.Context) {
	ctx := c.Request.Context()

	health, _, err := app.models.Platform.DatabaseHealth(ctx)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	lastBackup, err := app.models.Platform.LastBackup(ctx)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"backups": backupStatus(health, lastBackup, time.Now())}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// Backup freshness thresholds (system-design.txt 8.3: continuous WAL
// archiving plus a weekly base backup).
const (
	walStaleAfter        = 24 * time.Hour
	baseBackupStaleAfter = 8 * 24 * time.Hour
)

// backupStatus summarises the backups of system-design.txt 8.3: the last
// base backup (as recorded by scripts/db_backup_verify.sh, with its size)
// and continuous WAL archiving.
//
//	not_configured  nothing archived or recorded yet (e.g. local development)
//	failing         the most recent WAL archive attempt failed
//	stale           no WAL archived within 24h, or no base backup within 8 days
//	healthy         otherwise
func backupStatus(h db.DatabaseHealth, last *data.BackupRecord, now time.Time) envelope {
	status := "healthy"
	switch {
	case h.WALArchivedCount == 0 && h.WALFailedCount == 0 && last == nil:
		status = "not_configured"
	case h.LastFailedTime != nil && (h.LastArchivedTime == nil || h.LastFailedTime.After(*h.LastArchivedTime)):
		status = "failing"
	case h.LastArchivedTime == nil || now.Sub(*h.LastArchivedTime) > walStaleAfter:
		status = "stale"
	case last == nil || now.Sub(last.CompletedAt) > baseBackupStaleAfter:
		status = "stale"
	}

	return envelope{
		"status":           status,
		"last_base_backup": last,
		"wal_archiving": envelope{
			"last_successful_at": h.LastArchivedTime,
			"last_archived_wal":  h.LastArchivedWAL,
			"archived_count":     h.WALArchivedCount,
			"failed_count":       h.WALFailedCount,
			"last_failed_at":     h.LastFailedTime,
		},
	}
}
