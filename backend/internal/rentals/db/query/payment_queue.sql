-- The manager review queue for inbound payments, and payment-intent upkeep
-- (system-design.txt 4.7, 4.8, 5). Tenant-scoped under RLS.

-- ListPaymentsForReview is everything a manager still has to look at: payments
-- that are unmatched, matched but not placed, or placed by the engine on a
-- guess that is not yet confirmed. Newest first.
-- name: ListPaymentsForReview :many
SELECT
    count(*) OVER() AS total_records,
    p.id, p.source, p.mpesa_receipt, p.amount, p.msisdn, p.payer_name,
    p.account_reference, p.matched_unit_id, p.status,
    p.auto_applied_unconfirmed, p.review_note, p.received_at,
    u.unit_code
FROM payments p
LEFT JOIN units u ON u.id = p.matched_unit_id
WHERE p.tenant_id = @tenant_id
  AND (p.status <> 'allocated' OR p.auto_applied_unconfirmed)
ORDER BY p.received_at DESC, p.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- CountPendingPaymentsForPeriod is the sanity check's "no unallocated or
-- review-queue payments left for the period": payments matched to one of
-- the property's units, received within the period, that are not a
-- confirmed allocation.
-- name: CountPendingPaymentsForPeriod :one
SELECT count(*)
FROM payments p
INNER JOIN units u ON u.id = p.matched_unit_id
WHERE p.tenant_id = @tenant_id
  AND u.property_id = @property_id
  AND (p.received_at AT TIME ZONE 'Africa/Nairobi')::date >= sqlc.arg(period)::date
  AND (p.received_at AT TIME ZONE 'Africa/Nairobi')::date < (sqlc.arg(period)::date + interval '1 month')
  AND (p.status <> 'allocated' OR p.auto_applied_unconfirmed);

-- name: GetPayment :one
SELECT * FROM payments
WHERE tenant_id = $1 AND id = $2;

-- ConfirmPayment clears the unconfirmed flag once a manager has checked the
-- engine placement, and settles the payment: one placed on a guess is stored as
-- 'matched' until then, and would otherwise stay in the review list for ever.
-- Zero rows means there was nothing to confirm.
-- name: ConfirmPayment :execrows
UPDATE payments
SET auto_applied_unconfirmed = false, review_note = NULL,
    status = CASE WHEN status = 'matched' THEN 'allocated' ELSE status END
WHERE tenant_id = $1 AND id = $2 AND auto_applied_unconfirmed;

-- ExpireStaleIntents closes intents whose STK push window has passed.
-- name: ExpireStaleIntents :execrows
UPDATE payment_intents
SET status = 'expired'
WHERE tenant_id = $1 AND status = 'pending' AND expires_at < now();

-- SetPaymentIntentCheckout records PayHero checkout request id for a push.
-- name: SetPaymentIntentCheckout :exec
UPDATE payment_intents
SET checkout_request_id = $3
WHERE tenant_id = $1 AND id = $2;

-- GetPaymentForIntent is the payment that settled an intent: the tenant
-- receipt shown when the pay page sees the intent complete.
-- name: GetPaymentForIntent :one
SELECT * FROM payments
WHERE tenant_id = $1 AND matched_intent_id = $2
ORDER BY received_at DESC
LIMIT 1;
