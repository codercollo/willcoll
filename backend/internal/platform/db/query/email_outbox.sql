-- Owner-alert outbox (migration 000022, C3.7). Enqueue runs in a tenant
-- transaction; claim/settle/stats are cross-tenant and run on the admin pool.

-- EnqueueEmail is idempotent per (manager_id, event_key): a second enqueue of
-- the same event inserts nothing (no rows returned).
-- name: EnqueueEmail :execrows
INSERT INTO email_outbox (manager_id, event_key, recipient, template, payload)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (manager_id, event_key) DO NOTHING;

-- ClaimDueEmails leases up to $1 due rows: attempts is counted up front and
-- next_attempt_at pushed out by the lease ($2 seconds), so a worker that dies
-- mid-send simply has the row retried when the lease lapses. SKIP LOCKED lets
-- several workers (or instances) share the queue without double-sending.
-- name: ClaimDueEmails :many
UPDATE email_outbox
SET attempts = attempts + 1,
    next_attempt_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int)
WHERE id IN (
    SELECT o.id FROM email_outbox o
    WHERE o.status = 'pending' AND o.next_attempt_at <= now()
    ORDER BY o.created_at
    LIMIT sqlc.arg(batch)::int
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkEmailSent :exec
UPDATE email_outbox SET status = 'sent', sent_at = now(), last_error = NULL WHERE id = $1;

-- MarkEmailFailed records a failed attempt. With attempts left the row stays
-- pending and is retried after $3 seconds; with none left it becomes 'dead'
-- and is never retried.
-- name: MarkEmailFailed :exec
UPDATE email_outbox
SET last_error = $2,
    status = CASE WHEN attempts >= sqlc.arg(max_attempts)::int THEN 'dead' ELSE 'pending' END,
    next_attempt_at = now() + make_interval(secs => sqlc.arg(retry_seconds)::int)
WHERE id = $1;

-- OutboxStats backs the System page.
-- name: OutboxStats :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::int AS pending,
    count(*) FILTER (WHERE status = 'dead')::int AS dead,
    count(*) FILTER (WHERE status = 'sent' AND sent_at > now() - interval '24 hours')::int AS sent_24h,
    COALESCE(EXTRACT(EPOCH FROM now() - min(created_at) FILTER (WHERE status = 'pending')), 0)::float8 AS oldest_pending_age_seconds,
    (SELECT o2.last_error FROM email_outbox o2 WHERE o2.last_error IS NOT NULL
        ORDER BY o2.next_attempt_at DESC LIMIT 1) AS last_error
FROM email_outbox;
