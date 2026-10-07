-- Platform facts that live outside Postgres, recorded here so the Super
-- Admin dashboard can read them (system-design.txt 4.9, 8.3). The first
-- key is 'last_backup', written by scripts/db_backup_verify.sh after it
-- inspects the backup repository: {"completed_at", "size_bytes", "tool",
-- "label", "verified_at"}. Postgres can't report a base backup's size
-- itself; the repository (pgBackRest / S3) can.
--
-- Platform-level: no tenant_id, no RLS. Only willcoll_admin, the role the
-- backup cron and /v1/admin/* use, may write it; the tenant-facing API
-- role may not even read it.
CREATE TABLE IF NOT EXISTS system_metadata (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

REVOKE ALL ON system_metadata FROM willcoll_app;
GRANT SELECT, INSERT, UPDATE ON system_metadata TO willcoll_admin;
