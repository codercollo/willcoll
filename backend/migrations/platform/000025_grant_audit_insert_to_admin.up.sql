-- willcoll_platform_admin may append to audit_log (C4.1). `devtools
-- authz-backfill --apply` runs cross-tenant on the admin role and must write one
-- system-actor audit row per change in the same transaction as the change.
-- INSERT only: the trail stays append-only (no UPDATE, no DELETE), the same
-- shape as 000016's narrow grant on staff.
GRANT INSERT ON audit_log TO willcoll_platform_admin;
