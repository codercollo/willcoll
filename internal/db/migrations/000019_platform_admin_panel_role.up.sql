-- source distinguishes a support-triggered platform-admin write from a
-- normal Manager/system-originated audit_log entry (spec Phase 23.6). NULL
-- (via the DEFAULT) means "not platform admin" for every pre-existing row.
ALTER TABLE audit_log ADD COLUMN source TEXT NOT NULL DEFAULT 'system';

-- Dedicated Postgres role for the standalone platform admin panel binary
-- (cmd/willcoll-admin, spec Phase 23.2). BYPASSRLS, LOGIN — the one
-- deliberate place outside internal/tenancy.WithPlatformAdmin where RLS is
-- bypassed, same narrow-exception pattern as the public branding endpoint's
-- unauthenticated read, not a general admin shortcut. Never shared with
-- internal/api's own pool/DSN — the admin binary connects with this role's
-- own credentials only.
CREATE ROLE platform_admin_panel LOGIN PASSWORD 'changeme_in_production' BYPASSRLS;

DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO platform_admin_panel', current_database());
END
$$;
GRANT USAGE ON SCHEMA public TO platform_admin_panel;

-- 23.4/23.5: read-mostly platform health + operational safety net.
GRANT SELECT ON organizations, organization_addons, audit_log, properties, units,
    ledger_accounts, ledger_entries, payment_gateway_transactions TO platform_admin_panel;

-- 23.6: the only write actions in the whole panel — subscription_tier and
-- billing_status on organizations, plus the audit_log entry every such write
-- must leave behind (source='platform_admin').
GRANT UPDATE (subscription_tier, billing_status, updated_at) ON organizations TO platform_admin_panel;
GRANT INSERT ON audit_log TO platform_admin_panel;
