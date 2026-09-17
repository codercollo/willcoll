-- ml-sidecar's Postgres role (spec §13.3.1): SELECT-only on the tables its
-- feature extraction reads, plus INSERT (never UPDATE) on property_scores,
-- which it owns exclusively — the one table it's allowed to write, mirroring
-- internal/money owning ledger_accounts.balance. RLS stays enforced for this
-- role (no BYPASSRLS granted), so it only ever sees/writes rows scoped by
-- app.current_org_id like every other caller.
CREATE ROLE ml_sidecar_readonly LOGIN PASSWORD 'changeme_in_production';

DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO ml_sidecar_readonly', current_database());
END
$$;
GRANT USAGE ON SCHEMA public TO ml_sidecar_readonly;
-- properties is not in spec §13.3.1's explicit list, but units/leases/invoices
-- don't carry organization_id directly — their RLS policies subquery
-- properties.organization_id to check it, and a policy subquery runs under
-- the querying role's own privileges, not the table owner's. Without SELECT
-- here, RLS on every table below fails closed with "permission denied",
-- not just an empty result — verified against a real Postgres instance.
GRANT SELECT ON properties, ledger_entries, ledger_accounts, invoices, leases, units, meter_readings TO ml_sidecar_readonly;
GRANT SELECT, INSERT ON property_scores TO ml_sidecar_readonly;

-- The sidecar's endpoint only ever receives property_id (spec §13.5), never
-- an organization_id — but RLS on every table above needs app.current_org_id
-- set before it will return anything. resolve_property_org is the one
-- narrow, SECURITY DEFINER bootstrap: it reads only properties.organization_id
-- (a foreign-key fact, not ledger data) to let the caller set that session
-- variable itself; it grants no other RLS bypass.
CREATE FUNCTION resolve_property_org(p_property_id UUID)
RETURNS UUID
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT organization_id FROM properties WHERE id = p_property_id;
$$;

REVOKE ALL ON FUNCTION resolve_property_org(UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION resolve_property_org(UUID) TO ml_sidecar_readonly;
