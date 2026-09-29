-- Platform billing: a manager pays Willcoll a subscription through PayHero
-- (system-design.txt 3.6, 4.8, 7b). Plans are global; subscriptions and
-- their invoices are scoped to the manager (the tenant) under RLS.

-- ListSubscriptionPlans is what a manager may subscribe to: archived plans
-- never show, and a TEST plan (is_test) only shows when include_test is set
-- (the API sets it only in development — cmd/api's source of truth, not the
-- frontend).
-- name: ListSubscriptionPlans :many
SELECT * FROM subscription_plans
WHERE archived_at IS NULL AND (is_test = false OR sqlc.arg(include_test)::bool)
ORDER BY sort_order, price, name;

-- name: GetSubscriptionPlan :one
SELECT * FROM subscription_plans WHERE id = $1;

-- name: GetSubscriptionByManager :one
SELECT * FROM subscriptions WHERE manager_id = $1;

-- name: CreateSubscription :one
INSERT INTO subscriptions (manager_id, plan_id, status, current_period_start, current_period_end)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- CreateSubscriptionInvoice opens a pending invoice. mpesa_receipt is unique
-- and not yet known, so it holds a placeholder until the payment arrives.
-- plan_id is the plan the manager chose for this renewal: it may differ from
-- the subscription's current plan_id, and is what the webhook activates once
-- the payment is confirmed.
-- name: CreateSubscriptionInvoice :one
INSERT INTO subscription_invoices (manager_id, subscription_id, plan_id, amount, period, mpesa_receipt, status)
VALUES ($1, $2, $3, $4, $5, $6, 'pending')
RETURNING *;

-- CountManagerUnits is the manager's occupied+vacant unit count across their
-- live properties, checked against a plan's unit_cap before a renewal.
-- name: CountManagerUnits :one
SELECT count(*)::int FROM units u
JOIN properties p ON p.id = u.property_id
WHERE u.tenant_id = $1 AND p.deleted_at IS NULL;

-- name: GetSubscriptionInvoice :one
SELECT * FROM subscription_invoices
WHERE manager_id = $1 AND id = $2;

-- name: ListSubscriptionInvoices :many
SELECT count(*) OVER() AS total_records, sqlc.embed(subscription_invoices)
FROM subscription_invoices
WHERE manager_id = @manager_id
ORDER BY created_at DESC, id ASC
LIMIT @page_limit OFFSET @page_offset;

-- ResolveSubscriptionInvoice is a CROSS-TENANT lookup for the subscriptions
-- webhook, which arrives with only the invoice reference. Runs on the
-- willcoll_admin pool (BYPASSRLS, SELECT only) and returns ids only.
-- name: ResolveSubscriptionInvoice :one
SELECT id, manager_id FROM subscription_invoices WHERE id = $1;

-- MarkSubscriptionInvoicePaid settles a pending or failed invoice. Zero rows
-- means it was already paid: a redelivered callback.
-- name: MarkSubscriptionInvoicePaid :execrows
UPDATE subscription_invoices
SET status = 'paid', mpesa_receipt = $3, paid_at = $4
WHERE manager_id = $1 AND id = $2 AND status <> 'paid';

-- MarkSubscriptionInvoiceFailed records a failed or cancelled push, leaving a
-- paid invoice alone.
-- name: MarkSubscriptionInvoiceFailed :execrows
UPDATE subscription_invoices
SET status = 'failed'
WHERE manager_id = $1 AND id = $2 AND status = 'pending';

-- ActivateSubscription starts a paid period, on the plan the paid invoice
-- named (a switch takes effect here, not at renewal request time).
-- name: ActivateSubscription :one
UPDATE subscriptions
SET status = 'active', plan_id = $3, current_period_start = $4, current_period_end = $5, version = version + 1
WHERE manager_id = $1 AND id = $2
RETURNING version;

-- name: GetManagerBillingContact :one
SELECT id, firm_name, email, phone FROM managers WHERE id = $1;

-- DevDeleteSubscription removes a manager's subscription (and, by cascade,
-- its invoices). Used only by cmd/devtools' dev/reset-trial, never by the API.
-- name: DevDeleteSubscription :execrows
DELETE FROM subscriptions WHERE manager_id = $1;
