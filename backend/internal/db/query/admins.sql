-- Super Admin (system-design.txt 1.1, 3.1, 4.9). Platform-level, outside
-- the tenant boundary.
--
-- The admin account queries (GetAdmin, GetAdminByEmail, UpsertAdmin) run on
-- the API's willcoll_app pool. The oversight queries further down run ONLY
-- on the willcoll_admin pool (BYPASSRLS, read-mostly — system-design.txt
-- 3.7), because they read across every tenant; under willcoll_app, RLS
-- would hide the tenant-scoped rows they count.

-- name: GetAdmin :one
SELECT * FROM admins
WHERE id = $1;

-- name: GetAdminByEmail :one
SELECT * FROM admins
WHERE email = $1;

-- UpsertAdmin provisions the one Super Admin from configuration at
-- startup. It conflicts on is_singleton (000023), so there is only ever
-- one row: a changed ADMIN_EMAIL updates it in place, keeping its id.
-- name: UpsertAdmin :one
INSERT INTO admins (name, email, password_hash)
VALUES ($1, $2, $3)
ON CONFLICT (is_singleton) DO UPDATE
SET name = EXCLUDED.name, email = EXCLUDED.email, password_hash = EXCLUDED.password_hash
RETURNING *;

-- ---------------------------------------------------------------- oversight

-- AdminListManagers pages every manager (firm) with its latest
-- subscription (NULL when it has none) and portfolio size
-- (GET /v1/admin/managers).
-- name: AdminListManagers :many
SELECT count(*) OVER() AS total_records,
       sqlc.embed(managers),
       -- sqlc can't infer that these LEFT JOIN columns are nullable, so a
       -- firm with no subscription yields '' and 0001-01-01; the data layer
       -- maps both back to "none".
       COALESCE(latest.status, '')::text AS subscription_status,
       COALESCE(latest.current_period_end, '0001-01-01')::date AS subscription_period_end,
       (SELECT count(*) FROM properties p
         WHERE p.tenant_id = managers.id AND p.deleted_at IS NULL)::int AS property_count,
       (SELECT count(*) FROM units u
         JOIN properties p ON p.id = u.property_id
         WHERE u.tenant_id = managers.id AND p.deleted_at IS NULL)::int AS unit_count
FROM managers
LEFT JOIN (
    SELECT DISTINCT ON (manager_id) manager_id, status, current_period_end
    FROM subscriptions
    ORDER BY manager_id, current_period_end DESC
) latest ON latest.manager_id = managers.id
WHERE (sqlc.narg('status')::text IS NULL OR managers.status = sqlc.narg('status')::text)
ORDER BY
    CASE WHEN @sort::text = 'firm_name' THEN managers.firm_name END ASC,
    CASE WHEN @sort::text = '-firm_name' THEN managers.firm_name END DESC,
    CASE WHEN @sort::text = 'created_at' THEN managers.created_at END ASC,
    CASE WHEN @sort::text = '-created_at' THEN managers.created_at END DESC,
    managers.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- AdminGetManager is one manager with the same oversight columns.
-- name: AdminGetManager :one
SELECT sqlc.embed(managers),
       -- sqlc can't infer that these LEFT JOIN columns are nullable, so a
       -- firm with no subscription yields '' and 0001-01-01; the data layer
       -- maps both back to "none".
       COALESCE(latest.status, '')::text AS subscription_status,
       COALESCE(latest.current_period_end, '0001-01-01')::date AS subscription_period_end,
       (SELECT count(*) FROM properties p
         WHERE p.tenant_id = managers.id AND p.deleted_at IS NULL)::int AS property_count,
       (SELECT count(*) FROM units u
         JOIN properties p ON p.id = u.property_id
         WHERE u.tenant_id = managers.id AND p.deleted_at IS NULL)::int AS unit_count
FROM managers
LEFT JOIN (
    SELECT DISTINCT ON (manager_id) manager_id, status, current_period_end
    FROM subscriptions
    ORDER BY manager_id, current_period_end DESC
) latest ON latest.manager_id = managers.id
WHERE managers.id = $1;

-- AdminListSubscriptions pages every firm's subscriptions
-- (GET /v1/admin/subscriptions), soonest-ending first by default.
-- name: AdminListSubscriptions :many
SELECT count(*) OVER() AS total_records,
       sqlc.embed(subscriptions),
       managers.firm_name,
       subscription_plans.name AS plan_name,
       subscription_plans.price AS plan_price,
       subscription_plans.billing_interval AS plan_billing_interval
FROM subscriptions
JOIN managers ON managers.id = subscriptions.manager_id
JOIN subscription_plans ON subscription_plans.id = subscriptions.plan_id
WHERE (sqlc.narg('status')::text IS NULL OR subscriptions.status = sqlc.narg('status')::text)
ORDER BY
    CASE WHEN @sort::text = 'current_period_end' THEN subscriptions.current_period_end END ASC,
    CASE WHEN @sort::text = '-current_period_end' THEN subscriptions.current_period_end END DESC,
    subscriptions.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- GetSystemMetadata reads a platform fact recorded outside the API, e.g.
-- 'last_backup' from scripts/db_backup_verify.sh (migration 000022).
-- name: GetSystemMetadata :one
SELECT * FROM system_metadata
WHERE key = $1;

-- Database size, largest tables and WAL-archiver health are read by
-- db.SQLStore.DatabaseHealth: sqlc has no types for the pg_stat_* views.
