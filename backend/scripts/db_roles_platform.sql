-- One-time, per-environment creation of willcoll_platform's runtime roles
-- (system-design.txt A.2, mirrors scripts/db_roles.sql for willcoll_rentals).
-- Run via a platform db/roles target, as a role that can create roles and
-- grant BYPASSRLS (i.e. a superuser), against the willcoll_platform
-- database. Idempotent: re-running it resets the passwords and attributes.
-- Privileges are granted separately by migrations/platform's
-- 000013_grant_app_roles.
--
-- Usage: psql <superuser DSN, willcoll_platform> \
--          -v app_password=... -v admin_password=... -f scripts/db_roles_platform.sql

\set ON_ERROR_STOP on

SELECT 'CREATE ROLE willcoll_platform_app'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_platform_app') \gexec

SELECT 'CREATE ROLE willcoll_platform_admin'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'willcoll_platform_admin') \gexec

SELECT format('ALTER ROLE willcoll_platform_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD %L', :'app_password') \gexec

SELECT format('ALTER ROLE willcoll_platform_admin WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS PASSWORD %L', :'admin_password') \gexec
