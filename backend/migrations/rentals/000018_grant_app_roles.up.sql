-- Privileges for the two runtime roles (system-design.txt 3.7). The roles
-- themselves are cluster-level and carry per-environment passwords, so
-- they're created once per environment by `make db/roles`
-- (scripts/db_roles.sql), not here — this migration only grants.
--
--   willcoll_app   — the API's everyday role. NOSUPERUSER, NOBYPASSRLS, not
--                    the table owner, so every RLS policy from 000017
--                    applies to it. Superusers bypass RLS even under FORCE
--                    ROW LEVEL SECURITY, which is why the API must never
--                    connect as the migration role.
--   willcoll_admin — BYPASSRLS, used ONLY by /v1/admin/* handlers for
--                    read-mostly platform oversight. Never writes into a
--                    manager's tenant-scoped tables.

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_app')
       OR NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_admin') THEN
        RAISE EXCEPTION 'roles willcoll_app / willcoll_admin do not exist; run `make db/roles` first';
    END IF;
END $$;

GRANT USAGE ON SCHEMA public TO willcoll_app, willcoll_admin;

-- willcoll_app: full DML on everything...
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO willcoll_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO willcoll_app;

-- ...except the append-only ledger core (ADR 0002): corrections are storno
-- reversals, never UPDATE/DELETE, and now the database enforces that too.
-- (ON DELETE CASCADE from managers still works: referential actions run
-- with the table owner's privileges, not the caller's.)
REVOKE UPDATE, DELETE ON charges, transaction_headers, ledger_entries, payment_allocations FROM willcoll_app;

-- The migration bookkeeping table and the platform plan catalog aren't the
-- API's to change.
REVOKE ALL ON schema_migrations FROM willcoll_app;
REVOKE INSERT, UPDATE, DELETE ON subscription_plans FROM willcoll_app;

-- willcoll_admin: read everything; write only platform-level state
-- (suspending a manager, subscription status, the plan catalog, and
-- revoking sessions on suspension).
GRANT SELECT ON ALL TABLES IN SCHEMA public TO willcoll_admin;
REVOKE ALL ON schema_migrations FROM willcoll_admin;
GRANT UPDATE ON managers, subscriptions TO willcoll_admin;
GRANT INSERT, UPDATE ON subscription_plans TO willcoll_admin;
GRANT DELETE ON sessions TO willcoll_admin;

-- Tables created by later migrations get the same baseline automatically.
-- A later append-only table must REVOKE UPDATE, DELETE in its own migration.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO willcoll_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO willcoll_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT ON TABLES TO willcoll_admin;
