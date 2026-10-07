-- Subscription plan management for the Super Admin (system-design.txt A.1,
-- A.7). subscription_plans is global (no tenant), one catalog for all three
-- products (told apart by `product`), and only willcoll_platform_admin may
-- write it — the app role can only read it (migration 000013). Plans are
-- never deleted: subscriptions reference them.

-- name: AdminCreateSubscriptionPlan :one
INSERT INTO subscription_plans (product, name, price, billing_interval, unit_cap, included_seats, features, is_test, pricing_type, per_unit_price, min_price, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- AdminUpdateSubscriptionPlan changes a plan. A new price applies to invoices
-- raised from then on; invoices already issued keep the amount they carry.
-- Product is fixed at creation, not editable here.
-- name: AdminUpdateSubscriptionPlan :one
UPDATE subscription_plans
SET name = $2, price = $3, billing_interval = $4, unit_cap = $5, included_seats = $6,
    features = $7, is_test = $8, pricing_type = $9, per_unit_price = $10, min_price = $11, sort_order = $12
WHERE id = $1
RETURNING *;

-- AdminArchiveSubscriptionPlan hides a plan from managers and blocks renewals
-- onto it; existing subscribers keep their period, and history is untouched.
-- name: AdminArchiveSubscriptionPlan :one
UPDATE subscription_plans SET archived_at = now() WHERE id = $1 AND archived_at IS NULL
RETURNING *;

-- AdminRestoreSubscriptionPlan un-archives a plan.
-- name: AdminRestoreSubscriptionPlan :one
UPDATE subscription_plans SET archived_at = NULL WHERE id = $1 AND archived_at IS NOT NULL
RETURNING *;

-- AdminListSubscriptionPlans lists every plan, across every product, with
-- how many subscriptions use it, so the admin sees what a change affects.
-- name: AdminListSubscriptionPlans :many
SELECT p.*, (SELECT count(*) FROM subscriptions s WHERE s.plan_id = p.id)::bigint AS subscribers
FROM subscription_plans p
ORDER BY p.product, p.sort_order, p.price, p.name;

-- name: GetSubscriptionPlan :one
SELECT * FROM subscription_plans
WHERE id = $1;
