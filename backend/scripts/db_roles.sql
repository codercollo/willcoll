-- One-time, per-environment creation of the API's runtime roles
-- (system-design.txt 3.7). Run via `make db/roles`, as a role that can
-- create roles and grant BYPASSRLS (i.e. a superuser). Idempotent: re-running
-- it resets the passwords and attributes. Privileges are granted separately
-- by migration 000018_grant_app_roles.
--
-- Usage: psql <superuser DSN> -v app_password=... -v admin_password=... -f scripts/db_roles.sql

\set ON_ERROR_STOP on

SELECT 'CREATE ROLE willcoll_app'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_app') \gexec

SELECT 'CREATE ROLE willcoll_admin'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_admin') \gexec

SELECT format('ALTER ROLE willcoll_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD %L', :'app_password') \gexec

SELECT format('ALTER ROLE willcoll_admin WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS PASSWORD %L', :'admin_password') \gexec
