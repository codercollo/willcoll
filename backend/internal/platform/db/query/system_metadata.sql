-- GetSystemMetadata reads a platform fact recorded outside the API, e.g.
-- 'last_backup' from scripts/db_backup_verify.sh.
-- name: GetSystemMetadata :one
SELECT * FROM system_metadata
WHERE key = $1;
