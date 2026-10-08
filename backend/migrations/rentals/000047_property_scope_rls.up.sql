-- Property scope (docs/security/authorization.md section 3, D2, build C2).
--
-- A staff member restricted to some properties has the API open every tenant
-- transaction with `app.property_ids` set to those property ids (comma
-- separated; internal/shared/auth/scope.go, internal/rentals/db/store.go).
-- These RESTRICTIVE policies are ANDed with tenant_isolation, so a scoped
-- caller sees, and can write, only rows that belong to an allowed property -
-- through every query: lists, dashboards, reports, exports, search, joins and
-- the unit_ledger_balances view (security_invoker). Out-of-scope rows simply
-- do not exist for that caller, which is why the API answers 404 for them.
--
-- Unset or empty app.property_ids means unrestricted: the firm login, owners,
-- staff with no property list, and system work (webhooks, jobs, the pay
-- surface). The policies cost a single setting check for those callers.
--
-- Tables are chained through their parents (leases -> units -> properties)
-- instead of copying property_id everywhere: the parent's own policy applies
-- inside the sub-select, so one rule governs each hop.
--
-- NOT scoped, deliberately:
--   transaction_headers  a header groups entries and carries no tenant/unit
--                        data (type, key, description, actor); entries and
--                        everything with money or people ARE scoped.
--   pay_otps             written and read only by the unauthenticated pay
--                        surface, which has no staff principal.
--   system_metadata      no tenant data.

CREATE OR REPLACE FUNCTION app_property_scope_active() RETURNS boolean
LANGUAGE sql STABLE AS
$$ SELECT coalesce(current_setting('app.property_ids', true), '') <> '' $$;

CREATE OR REPLACE FUNCTION app_property_in_scope(pid uuid) RETURNS boolean
LANGUAGE sql STABLE AS
$$ SELECT NOT app_property_scope_active()
       OR (pid IS NOT NULL
           AND pid = ANY (string_to_array(current_setting('app.property_ids', true), ',')::uuid[])) $$;

-- Rows that name their property directly.
CREATE POLICY property_scope ON properties AS RESTRICTIVE
    USING (app_property_in_scope(id));
CREATE POLICY property_scope ON units AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON garbage_runs AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON reports AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON unit_qr_codes AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON report_confirmations AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON report_confirmation_history AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON property_meter_readings AS RESTRICTIVE
    USING (app_property_in_scope(property_id));
CREATE POLICY property_scope ON receipt_counters AS RESTRICTIVE
    USING (app_property_in_scope(property_id));

-- Rows reached through a parent; the parent's own policy applies inside the sub-select.
CREATE POLICY property_scope ON leases AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON ledger_accounts AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON water_readings AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON rent_runs AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON payment_intents AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON receipts AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON arrears_commitments AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON payments AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR matched_unit_id IN (SELECT id FROM units));
CREATE POLICY property_scope ON lease_payers AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR lease_id IN (SELECT id FROM leases));
CREATE POLICY property_scope ON charges AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR ledger_account_id IN (SELECT id FROM ledger_accounts));
CREATE POLICY property_scope ON ledger_entries AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR ledger_account_id IN (SELECT id FROM ledger_accounts));
CREATE POLICY property_scope ON payment_allocations AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR payment_id IN (SELECT id FROM payments));

-- A landlord is visible to a scoped caller when one of their visible
-- properties is theirs. (A scoped caller cannot create landlords or
-- properties: the API refuses, because the new row would be invisible.)
CREATE POLICY property_scope ON landlords AS RESTRICTIVE
    USING (NOT app_property_scope_active() OR id IN (SELECT landlord_id FROM properties));
