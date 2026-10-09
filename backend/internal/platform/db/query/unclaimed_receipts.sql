-- unclaimed_receipts (migration 000023): money received on a channel nobody
-- has claimed. No RLS and no tenant: a webhook has no tenant context.

-- RecordUnclaimedReceipt is idempotent per M-Pesa receipt: a redelivery
-- inserts nothing (0 rows affected).
-- name: RecordUnclaimedReceipt :execrows
INSERT INTO unclaimed_receipts (mpesa_receipt, channel_id, product, amount, msisdn, raw_payload)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (mpesa_receipt) DO NOTHING;

-- name: GetUnclaimedReceipt :one
SELECT * FROM unclaimed_receipts WHERE id = $1;

-- ListOpenUnclaimedReceipts is the System page's list, oldest first.
-- name: ListOpenUnclaimedReceipts :many
SELECT * FROM unclaimed_receipts
WHERE status = 'open'
ORDER BY received_at
LIMIT sqlc.arg(max_rows)::int;

-- name: UnclaimedReceiptStats :one
SELECT
    count(*)::int AS open_count,
    COALESCE(sum(amount), 0)::numeric(12,2) AS open_amount,
    COALESCE(EXTRACT(EPOCH FROM now() - min(received_at)), 0)::float8 AS oldest_age_seconds
FROM unclaimed_receipts
WHERE status = 'open';

-- ResolveUnclaimedReceipt closes an open row. Scoped to status = 'open', so
-- resolving twice returns no row and the first resolution is never overwritten.
-- name: ResolveUnclaimedReceipt :one
UPDATE unclaimed_receipts
SET status = 'resolved', resolved_by = $2, resolved_at = now(), resolution_note = $3
WHERE id = $1 AND status = 'open'
RETURNING *;
