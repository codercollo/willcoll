package main

import (
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/gin-gonic/gin"
)

// Super Admin endpoints (system-design.txt 1.1, 4.9), all behind
// requireAdmin. Their data comes from app.models.Platform, which runs as
// willcoll_admin (BYPASSRLS, read-mostly — 3.7): the Super Admin sees
// every firm's subscription state and platform health, and never writes a
// manager's tenant data.

// adminSystemHealthHandler handles GET /v1/admin/system/health: database
// connectivity, size and largest tables, and the backup indicator
// (system-design.txt 4.9, 8.3). Responds 503 when the database can't be
// reached, so an uptime check can alert on the status code alone.
//
// Disk headroom: the database's own growth is reported here (size, top
// tables). Free space on the database host's disk isn't visible from the
// API container; that belongs to host monitoring (e.g. node-exporter).
//
// This is rentals-api's own report, read by platform-api's GET
// /v1/admin/system/health (the Super Admin page), which forwards the admin's
// session cookie and shows this alongside the other services.
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
		"backups":             backups,
		"usage_reporting":     app.usageReportingStatus(),
		"audit_failures_24h":  app.auditFailures.count24h(time.Now()),
		"inbox_fallbacks_24h": app.inboxFallbacks.count24h(time.Now()),
		"unclaimed_channels":  app.unclaimedChannels.list(time.Now()),
		"otp_ceiling_firms":   app.otpCeilings.list(time.Now()),
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
