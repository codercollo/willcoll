-- Subscription plan management for the Super Admin (system-design.txt 1.1,
-- 3.6). subscription_plans is global (no tenant), and only the willcoll_admin
-- role may write it; the API role can only read it. Plans are never deleted:
-- subscriptions reference them.

-- name: AdminCreateSubscriptionPlan :one
INSERT INTO subscription_plans (name, price, billing_interval, unit_cap, is_test, pricing_type, per_unit_price, min_price, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- AdminUpdateSubscriptionPlan changes a plan. A new price applies to invoices
-- raised from then on; invoices already issued keep the amount they carry.
-- name: AdminUpdateSubscriptionPlan :one
UPDATE subscription_plans
SET name = $2, price = $3, billing_interval = $4, unit_cap = $5, is_test = $6,
    pricing_type = $7, per_unit_price = $8, min_price = $9, sort_order = $10
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

-- AdminPlanSubscriberCount is how many subscriptions use a plan, so the admin
-- sees what a change affects.
-- name: AdminListSubscriptionPlans :many
SELECT p.*, (SELECT count(*) FROM subscriptions s WHERE s.plan_id = p.id)::bigint AS subscribers
FROM subscription_plans p
ORDER BY p.sort_order, p.price, p.name;
