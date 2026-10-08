-- Property scope fails closed (docs/security/authorization.md section 3a, C2
-- follow-up proofs).
--
-- Migration 000047 treated a missing or empty app.property_ids as
-- "unrestricted", so a code path that forgot to set the scope silently saw
-- every property. Now unrestricted must be said out loud: a transaction is
-- unrestricted only when `app.property_scope` is exactly 'all'. Anything else
-- (setting missing, empty, 'list', garbage) is restricted to the ids in
-- `app.property_ids`, and a restricted caller with no ids sees no rows at all.
--
-- The API sets both settings, with set_config(..., is_local => true), as the
-- first statements of every tenant transaction (internal/rentals/db/store.go):
--   unrestricted caller: app.property_scope = 'all'
--   scoped caller:       app.property_scope = 'list', app.property_ids = '<ids>'
--
-- The 000047 policies call these two functions, so redefining them is enough.

CREATE OR REPLACE FUNCTION app_property_scope_active() RETURNS boolean
LANGUAGE sql STABLE AS
$$ SELECT coalesce(current_setting('app.property_scope', true), '') <> 'all' $$;

CREATE OR REPLACE FUNCTION app_property_in_scope(pid uuid) RETURNS boolean
LANGUAGE sql STABLE AS
$$ SELECT NOT app_property_scope_active()
       OR (pid IS NOT NULL
           AND pid = ANY (string_to_array(nullif(current_setting('app.property_ids', true), ''), ',')::uuid[])) $$;
