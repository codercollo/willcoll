-- Proof that row-level security applies to the roles the APIs connect as.
-- Read-only (estates and waste are empty and have no migrations yet, so only
-- rentals and platform are checked): run it as the superuser after the migrations (and ideally after
-- seeding, so there are rows to hide):
--   psql -U postgres -v ON_ERROR_STOP=1 -f /opt/willcoll/check_rls.sql
-- Any failed expectation raises an exception and psql exits non-zero.
--
-- Why this exists: a superuser, a BYPASSRLS role or a table's owner is exempt
-- from RLS, so an API that connects as one of them silently loses tenant
-- isolation (CLAUDE.md "Two DSNs, never swapped"). Per database this checks:
--   1. the app role is neither superuser nor BYPASSRLS, and not the owner of
--      the database or of any table;
--   2. it cannot change the schema;
--   3. the database has RLS-protected tables, and, acting as the app role,
--      with a tenant that owns nothing, every one of them shows ZERO rows
--      even though the superuser sees data (the behavioural proof; if the
--      database is still empty it says so instead of passing quietly).

\set ON_ERROR_STOP on
\set QUIET on

\echo ---- willcoll_rentals
\c willcoll_rentals
\set app_role willcoll_app
\set admin_role willcoll_admin
\i /opt/willcoll/check_rls_one.sql

\echo ---- willcoll_platform
\c willcoll_platform
\set app_role willcoll_platform_app
\set admin_role willcoll_platform_admin
\i /opt/willcoll/check_rls_one.sql

\echo OK: row-level security applies to every API role
