-- Platform facts that live outside Postgres (system-design.txt A.10): the
-- Super Admin dashboard's backup indicator. Platform-level: no tenant_id,
-- no RLS. Only willcoll_platform_admin (scripts/db_backup_verify.sh and
-- /v1/admin/* on platform-api) may write it; the tenant-facing app role
-- may not even read it.
CREATE TABLE IF NOT EXISTS system_metadata (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
