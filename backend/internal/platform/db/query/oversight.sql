-- Super Admin oversight (build-phases.txt 3i-b; system-design.txt 1.1, 4.9).
-- Moved from internal/rentals/db/query/admins.sql. Everything here reads
-- willcoll_platform ONLY — firm, modules, per-product subscriptions, staff
-- and module_usage counts — never a product database. These run ONLY on
-- the willcoll_platform_admin pool (BYPASSRLS): they read across every
-- tenant, and under willcoll_platform_app RLS would hide the rows.

-- OversightListManagers pages every manager (firm) with its latest
-- subscription across products (NULL when it has none), its staff count
-- and its Rentals unit count (module_usage, as the product reported it).
-- name: OversightListManagers :many
SELECT count(*) OVER() AS total_records,
       sqlc.embed(managers),
       -- sqlc can't infer that these LEFT JOIN columns are nullable, so a
       -- firm with no subscription yields '' and 0001-01-01; the data layer
       -- maps both back to "none".
       COALESCE(latest.status, '')::text AS subscription_status,
       COALESCE(latest.current_period_end, '0001-01-01')::date AS subscription_period_end,
       (SELECT count(*) FROM staff s WHERE s.manager_id = managers.id)::int AS staff_count,
       COALESCE((SELECT mu.billable_count FROM module_usage mu
                  WHERE mu.manager_id = managers.id AND mu.product = 'rentals'), 0)::int AS unit_count
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

-- OversightGetManager is one manager with the same oversight columns.
-- name: OversightGetManager :one
SELECT sqlc.embed(managers),
       COALESCE(latest.status, '')::text AS subscription_status,
       COALESCE(latest.current_period_end, '0001-01-01')::date AS subscription_period_end,
       (SELECT count(*) FROM staff s WHERE s.manager_id = managers.id)::int AS staff_count,
       COALESCE((SELECT mu.billable_count FROM module_usage mu
                  WHERE mu.manager_id = managers.id AND mu.product = 'rentals'), 0)::int AS unit_count
FROM managers
LEFT JOIN (
    SELECT DISTINCT ON (manager_id) manager_id, status, current_period_end
    FROM subscriptions
    ORDER BY manager_id, current_period_end DESC
) latest ON latest.manager_id = managers.id
WHERE managers.id = $1;

-- The three queries below attach the per-product detail to a page of
-- managers in one round trip each, instead of N+1 per firm.

-- name: OversightListModules :many
SELECT * FROM manager_modules
WHERE manager_id = ANY(@manager_ids::uuid[])
ORDER BY manager_id, product;

-- name: OversightListUsage :many
SELECT * FROM module_usage
WHERE manager_id = ANY(@manager_ids::uuid[])
ORDER BY manager_id, product;

-- name: OversightListManagerSubscriptions :many
SELECT sqlc.embed(subscriptions),
       subscription_plans.name AS plan_name
FROM subscriptions
JOIN subscription_plans ON subscription_plans.id = subscriptions.plan_id
WHERE subscriptions.manager_id = ANY(@manager_ids::uuid[])
ORDER BY subscriptions.manager_id, subscriptions.product, subscriptions.current_period_end DESC;

-- OversightListStaffIDs is every staff account of a manager, whatever its
-- status: suspending the firm signs all of them out.
-- name: OversightListStaffIDs :many
SELECT id FROM staff WHERE manager_id = $1;

-- OversightListSubscriptions pages every firm's subscriptions
-- (GET /v1/admin/subscriptions), soonest-ending first by default,
-- optionally filtered by product and/or status.
-- name: OversightListSubscriptions :many
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
  AND (sqlc.narg('product')::text IS NULL OR subscriptions.product = sqlc.narg('product')::text)
ORDER BY
    CASE WHEN @sort::text = 'current_period_end' THEN subscriptions.current_period_end END ASC,
    CASE WHEN @sort::text = '-current_period_end' THEN subscriptions.current_period_end END DESC,
    subscriptions.id ASC
LIMIT @page_limit OFFSET @page_offset;
