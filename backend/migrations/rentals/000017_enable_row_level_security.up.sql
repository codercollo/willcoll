-- Multi-tenancy enforcement (system-design.txt 3.7). Every tenant-scoped
-- table gets RLS keyed on `SET LOCAL app.tenant_id`, set as the first
-- statement in the request's transaction. The API connects as
-- willcoll_app (granted in 000018), which is neither the table owner nor a
-- superuser, so these policies apply to it. FORCE ROW LEVEL SECURITY
-- additionally subjects a non-superuser table owner to them. Note that no
-- flag can constrain a superuser or BYPASSRLS role -- which is exactly why
-- the API must never connect as the migration role.
--
-- subscription_plans is a platform-owned catalog with no tenant boundary
-- and is intentionally excluded. subscriptions / subscription_invoices are
-- keyed by manager_id rather than tenant_id (system-design.txt 3.6), so
-- their policy predicate differs accordingly.

DO $$
DECLARE
    tenant_id_table text;
    manager_id_table text;
BEGIN
    FOREACH tenant_id_table IN ARRAY ARRAY[
        'landlords', 'properties', 'units', 'leases', 'lease_payers',
        'ledger_accounts', 'water_readings', 'garbage_runs', 'rent_runs',
        'charges', 'transaction_headers', 'ledger_entries',
        'payment_intents', 'payments', 'payment_allocations',
        'receipts', 'reports'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tenant_id_table);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', tenant_id_table);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting(''app.tenant_id'')::uuid)',
            tenant_id_table
        );
    END LOOP;

    FOREACH manager_id_table IN ARRAY ARRAY['subscriptions', 'subscription_invoices']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', manager_id_table);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', manager_id_table);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (manager_id = current_setting(''app.tenant_id'')::uuid)',
            manager_id_table
        );
    END LOOP;
END $$;
