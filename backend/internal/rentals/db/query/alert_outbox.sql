-- Owner-alert outbox (migration 000049, C4.0). EnqueueAlert runs in the SAME
-- tenant transaction as the action it reports; the relay's queries are
-- cross-tenant and run on the admin pool.

-- EnqueueAlert is idempotent per (tenant_id, event_key): a second enqueue of
-- the same event inserts nothing (zero rows affected).
-- name: EnqueueAlert :execrows
INSERT INTO alert_outbox (tenant_id, event_key, payload)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, event_key) DO NOTHING;

-- ListPendingAlerts is the relay's batch: fewest failed attempts first, then
-- oldest. An alert the platform keeps refusing (its firm is gone) therefore
-- sinks behind new ones instead of filling every batch and starving them.
-- name: ListPendingAlerts :many
SELECT id, tenant_id, event_key, payload, attempts, created_at
FROM alert_outbox
WHERE status = 'pending' AND next_attempt_at <= now()
ORDER BY attempts, created_at
LIMIT sqlc.arg(batch)::int;

-- name: MarkAlertRelayed :exec
UPDATE alert_outbox
SET status = 'relayed', relayed_at = now(), last_error = NULL
WHERE id = $1 AND status = 'pending';

-- MarkAlertRelayFailed records a failed attempt and why. With attempts left the
-- row stays pending and is retried after retry_seconds; once max_attempts have
-- failed it becomes 'dead' and is never retried.
-- name: MarkAlertRelayFailed :exec
UPDATE alert_outbox
SET attempts = attempts + 1,
    last_error = sqlc.arg(last_error),
    status = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::int THEN 'dead' ELSE 'pending' END,
    next_attempt_at = now() + make_interval(secs => sqlc.arg(retry_seconds)::int)
WHERE id = sqlc.arg(id) AND status = 'pending';

-- AlertOutboxStats backs the System page: how many are waiting, for how long,
-- and how many ran out of attempts.
-- name: AlertOutboxStats :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::int AS pending,
    count(*) FILTER (WHERE status = 'dead')::int AS dead,
    coalesce(extract(epoch FROM now() - min(created_at) FILTER (WHERE status = 'pending')), 0)::int AS oldest_pending_age_seconds
FROM alert_outbox;
