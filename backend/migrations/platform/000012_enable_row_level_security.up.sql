-- Row-Level Security for the platform database, same mechanism as every
-- product database (system-design.txt A.2 / ADR-0003): SET LOCAL
-- app.tenant_id = the manager id, set as the first statement of a tenant
-- transaction on willcoll_platform_app. Not every table has a tenant
-- boundary here:
--
--   admins, managers, sessions, system_metadata, subscription_plans
--     platform-wide, no tenant boundary — intentionally excluded.
--   payment_inbox
--     arrives with no tenant context (a webhook names a channel, not a
--     manager) — the receiving product resolves manager_id itself, after
--     the claim, inside its own database. Intentionally excluded; every
--     write to it goes through willcoll_platform_app regardless of tenant.
--   tokens
--     intentionally excluded, same precedent as internal/rentals: a token
--     is looked up by its hash directly (GetManagerForToken joins on
--     tokens.hash + scope + expiry, never "list this tenant's tokens"),
--     and TokenModel's queries run on the Store directly, never inside
--     ExecTenantTx — there is no app.tenant_id set on that connection, so
--     an RLS policy here would fail with "unrecognized configuration
--     parameter" on every read or write (caught by an actual smoke test
--     of build-phases.txt 3c-ii, not left to be discovered in production).
--   service_links
--     scoped by collector_manager_id, not manager_id.
--   handoff_invoices
--     has no manager_id of its own; scoped through its service_links row.

DO $$
DECLARE
    manager_id_table text;
BEGIN
    FOREACH manager_id_table IN ARRAY ARRAY[
        'staff', 'subscriptions', 'subscription_invoices', 'manager_modules',
        'manager_addons', 'sms_wallets', 'sms_ledger', 'payment_channels',
        'audit_log'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', manager_id_table);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', manager_id_table);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (manager_id = current_setting(''app.tenant_id'')::uuid)',
            manager_id_table
        );
    END LOOP;
END $$;

-- service_links: the collector's own visibility. The linked party (the
-- estate/property side) grants or revokes consent through its own
-- product's API, which calls back into this table using the collector's
-- id it already holds from the link — not by querying this table under
-- its own tenant_id.
ALTER TABLE service_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON service_links
    USING (collector_manager_id = current_setting('app.tenant_id')::uuid);

-- handoff_invoices: scoped through its service_links row's collector.
--
-- KNOWN GAP, deliberately deferred to Phase 8 (the handoff workflow itself):
-- this predicate only lets the COLLECTOR (Waste) see its own handoff
-- invoices. The TARGET manager (the Estates/Rentals side, who must approve
-- and pay it from their own expense queue) has no row here to key off —
-- target_owner_id on service_links is an opaque id in another product's
-- database, and the platform schema has no manager_id for that side. Until
-- Phase 8 adds a resolvable target-manager mapping (most likely a
-- target_manager_id column on service_links, filled in when the target
-- grants consent), the target side's read/write path goes through
-- willcoll_platform_admin-equivalent access from within the target
-- product's own handler, not through this RLS policy. Do not build the
-- Estates/Rentals-facing handoff UI against this policy without revisiting
-- it first.
ALTER TABLE handoff_invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE handoff_invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON handoff_invoices USING (
    link_id IN (
        SELECT id FROM service_links
        WHERE collector_manager_id = current_setting('app.tenant_id')::uuid
    )
);
