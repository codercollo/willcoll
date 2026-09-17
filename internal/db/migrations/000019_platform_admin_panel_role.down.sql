REVOKE ALL PRIVILEGES ON organizations, organization_addons, audit_log, properties, units,
    ledger_accounts, ledger_entries, payment_gateway_transactions FROM platform_admin_panel;
REVOKE USAGE ON SCHEMA public FROM platform_admin_panel;
DROP ROLE IF EXISTS platform_admin_panel;

ALTER TABLE audit_log DROP COLUMN IF EXISTS source;
