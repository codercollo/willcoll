-- payment_inbox: deduplicates every M-Pesa receipt exactly once, before any
-- product-specific matching runs (system-design.txt A.6, build-phases.txt
-- 3g-2's claim -> post -> mark posted flow). No RLS: a webhook arrives with
-- no tenant context, so every statement here runs on willcoll_platform_app
-- directly, never inside ExecTenantTx (same precedent as TokenModel).

-- ClaimPaymentInbox is the "claim" step: ON CONFLICT DO NOTHING means a
-- retried webhook (PayHero redelivery, or a crash between claim and post)
-- never claims twice — the caller re-reads the existing row instead
-- (GetPaymentInboxByReceipt) to decide what already happened.
-- name: ClaimPaymentInbox :one
INSERT INTO payment_inbox (mpesa_receipt, channel_id, product, amount, msisdn, raw_payload)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (mpesa_receipt) DO NOTHING
RETURNING *;

-- name: GetPaymentInboxByReceipt :one
SELECT * FROM payment_inbox WHERE mpesa_receipt = $1;

-- MarkPaymentInboxPosted is the "mark posted" step, after the claiming
-- product successfully records the payment in its own database. Scoped to
-- status = 'claimed' so marking an already-posted row (a race with the
-- sweeper, build-phases.txt 3g-3) is a no-op, not an error.
-- name: MarkPaymentInboxPosted :execrows
UPDATE payment_inbox SET status = 'posted', posted_at = now()
WHERE mpesa_receipt = $1 AND status = 'claimed';

-- ListStaleClaimedPaymentInbox is the 5-minute sweeper's (build-phases.txt
-- 3g-3) own query: every row still 'claimed' for product, claimed before
-- cutoff — a crash between claim and post, or a process that died before
-- ever posting. Served by payment_inbox_claimed_pending_idx.
-- name: ListStaleClaimedPaymentInbox :many
SELECT * FROM payment_inbox
WHERE product = $1 AND status = 'claimed' AND claimed_at < $2
ORDER BY claimed_at;
