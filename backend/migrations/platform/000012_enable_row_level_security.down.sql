DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'staff', 'subscriptions', 'subscription_invoices', 'manager_modules',
        'manager_addons', 'sms_wallets', 'sms_ledger', 'payment_channels',
        'audit_log', 'service_links', 'handoff_invoices'
    ]
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('ALTER TABLE %I NO FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
    END LOOP;
END $$;
