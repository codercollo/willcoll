DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'landlords', 'properties', 'units', 'leases', 'lease_payers',
        'ledger_accounts', 'water_readings', 'garbage_runs', 'rent_runs',
        'charges', 'transaction_headers', 'ledger_entries',
        'payment_intents', 'payments', 'payment_allocations',
        'receipts', 'reports', 'subscriptions', 'subscription_invoices'
    ]
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('ALTER TABLE %I NO FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
    END LOOP;
END $$;
