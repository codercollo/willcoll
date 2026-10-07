-- GetSystemMetadata reads a platform fact recorded outside the API, e.g.
-- 'last_backup' from scripts/db_backup_verify.sh (migration 000022). Runs
-- on the willcoll_admin pool (the table is not readable by willcoll_app).
-- name: GetSystemMetadata :one
SELECT * FROM system_metadata
WHERE key = $1;

-- Database size, largest tables and WAL-archiver health are read by
-- db.SQLStore.DatabaseHealth: sqlc has no types for the pg_stat_* views.
