-- Privileges for the two runtime roles on willcoll_platform (mirrors
-- migrations 000018_grant_app_roles's rentals pattern, system-design.txt
-- A.2). Roles are cluster-level, created once per environment by
-- scripts/db_roles_platform.sql, not here — this migration only grants.
--
--   willcoll_platform_app   — every product API's (and platform-api's)
--                             everyday role. NOSUPERUSER, NOBYPASSRLS, not
--                             the table owner, so every RLS policy from
--                             000012 applies to it.
--   willcoll_platform_admin — BYPASSRLS, used only by platform-api's
--                             /v1/admin/* handlers for platform oversight.

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_platform_app')
       OR NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_platform_admin') THEN
        RAISE EXCEPTION 'roles willcoll_platform_app / willcoll_platform_admin do not exist; run scripts/db_roles_platform.sql first';
    END IF;
END $$;

GRANT USAGE ON SCHEMA public TO willcoll_platform_app, willcoll_platform_admin;

-- willcoll_platform_app: full DML on everything...
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO willcoll_platform_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO willcoll_platform_app;

-- ...except the append-only audit trail and SMS ledger (ADR-0002's
-- pattern, applied here too): a correction is a new row, never an
-- UPDATE/DELETE of history.
REVOKE UPDATE, DELETE ON audit_log, sms_ledger FROM willcoll_platform_app;

-- ...and the plan catalog and system_metadata, which aren't the app's to
-- change (mirrors 000018's treatment of subscription_plans there).
REVOKE ALL ON schema_migrations FROM willcoll_platform_app;
REVOKE INSERT, UPDATE, DELETE ON subscription_plans FROM willcoll_platform_app;
REVOKE ALL ON system_metadata FROM willcoll_platform_app;

-- willcoll_platform_admin: read everything; write only platform-level
-- state (suspending a manager, the plan catalog, system_metadata, and
-- revoking sessions on suspension).
GRANT SELECT ON ALL TABLES IN SCHEMA public TO willcoll_platform_admin;
REVOKE ALL ON schema_migrations FROM willcoll_platform_admin;
GRANT UPDATE ON managers, subscriptions, manager_modules TO willcoll_platform_admin;
GRANT INSERT, UPDATE ON subscription_plans TO willcoll_platform_admin;
GRANT SELECT, INSERT, UPDATE ON system_metadata TO willcoll_platform_admin;
GRANT DELETE ON sessions TO willcoll_platform_admin;

-- Tables created by later migrations get the same baseline automatically.
-- A later append-only table must REVOKE UPDATE, DELETE in its own migration.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO willcoll_platform_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO willcoll_platform_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT ON TABLES TO willcoll_platform_admin;
