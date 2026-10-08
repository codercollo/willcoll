-- First-boot setup of the Willcoll Postgres cluster. Reused as-is in
-- production: it holds no environment-specific value, every password is a
-- psql variable (see init.sh, which fills them from environment variables).
--
-- Four databases, one per product (estates and waste stay empty until their
-- phases). For each database:
--   <x>_owner  owns the database and runs its migrations. It is the ONLY role
--              that can change the schema, and it is never used by an API.
--              (BYPASSRLS: see below.)
--   app role   what the API connects as. NOSUPERUSER, NOBYPASSRLS and not the
--              owner of anything, so row-level security applies to it.
-- Rentals and platform also have an admin role (BYPASSRLS) for the few
-- cross-tenant jobs; those names are fixed by the existing migrations
-- (000013 grants to them by name), hence willcoll_app / willcoll_admin for
-- rentals and willcoll_platform_app / willcoll_platform_admin for platform.
--
-- Table privileges are NOT granted here: each product's migrations grant what
-- their roles need. check_rls.sql proves the result.
--
-- Run as the Postgres superuser, once, when the data directory is empty:
--   psql -v ON_ERROR_STOP=1 -v rentals_owner_pw=... (all 14 passwords) -f init.sql

\set ON_ERROR_STOP on

-- ---- roles ------------------------------------------------------------------

SELECT format('CREATE ROLE %I', r) FROM unnest(ARRAY[
    'willcoll_rentals_owner', 'willcoll_platform_owner', 'willcoll_estates_owner', 'willcoll_waste_owner',
    'willcoll_app', 'willcoll_admin', 'willcoll_platform_app', 'willcoll_platform_admin',
    'willcoll_estates_app', 'willcoll_waste_app'
]) AS r WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = r) \gexec

-- Owners run migrations. Every tenant table has FORCE ROW LEVEL SECURITY, so a
-- data-fixing migration (an UPDATE) run as a plain owner fails on the policy's
-- app.tenant_id setting (migration 000015 does exactly that). An owner is
-- therefore BYPASSRLS, as a superuser would be; it is used by `migrate` and
-- the seeder only, never by an API (check_rls.sql proves the API roles are not).
SELECT format('ALTER ROLE %I WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION BYPASSRLS PASSWORD %L', r, pw)
FROM (VALUES
    ('willcoll_rentals_owner',  :'rentals_owner_pw'),
    ('willcoll_platform_owner', :'platform_owner_pw'),
    ('willcoll_estates_owner',  :'estates_owner_pw'),
    ('willcoll_waste_owner',    :'waste_owner_pw')
) AS t(r, pw) \gexec

-- The API roles: RLS applies to them.
SELECT format('ALTER ROLE %I WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', r, pw)
FROM (VALUES
    ('willcoll_app',            :'rentals_app_pw'),
    ('willcoll_platform_app',   :'platform_app_pw'),
    ('willcoll_estates_app',    :'estates_app_pw'),
    ('willcoll_waste_app',      :'waste_app_pw')
) AS t(r, pw) \gexec

-- The two cross-tenant roles: the only ones allowed to bypass RLS.
SELECT format('ALTER ROLE %I WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION BYPASSRLS PASSWORD %L', r, pw)
FROM (VALUES
    ('willcoll_admin',          :'rentals_admin_pw'),
    ('willcoll_platform_admin', :'platform_admin_pw')
) AS t(r, pw) \gexec

-- ---- databases ----------------------------------------------------------------

SELECT format('CREATE DATABASE %I OWNER %I', db, owner)
FROM (VALUES
    ('willcoll_rentals',  'willcoll_rentals_owner'),
    ('willcoll_platform', 'willcoll_platform_owner'),
    ('willcoll_estates',  'willcoll_estates_owner'),
    ('willcoll_waste',    'willcoll_waste_owner')
) AS t(db, owner) WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = db) \gexec

-- Only the roles of a database may connect to it.
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', db) FROM unnest(ARRAY[
    'willcoll_rentals', 'willcoll_platform', 'willcoll_estates', 'willcoll_waste']) AS db \gexec

SELECT format('GRANT CONNECT ON DATABASE %I TO %s', db, roles)
FROM (VALUES
    ('willcoll_rentals',  'willcoll_app, willcoll_admin'),
    ('willcoll_platform', 'willcoll_platform_app, willcoll_platform_admin'),
    ('willcoll_estates',  'willcoll_estates_app'),
    ('willcoll_waste',    'willcoll_waste_app')
) AS t(db, roles) \gexec
