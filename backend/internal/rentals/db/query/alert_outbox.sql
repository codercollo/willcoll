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
WHERE status = 'pending'
ORDER BY attempts, created_at
LIMIT sqlc.arg(batch)::int;

-- name: MarkAlertRelayed :exec
UPDATE alert_outbox
SET status = 'relayed', relayed_at = now(), last_error = NULL
WHERE id = $1 AND status = 'pending';

-- MarkAlertRelayFailed leaves the row pending (it is retried on the next tick)
-- and records why.
-- name: MarkAlertRelayFailed :exec
UPDATE alert_outbox
SET attempts = attempts + 1, last_error = $2
WHERE id = $1 AND status = 'pending';

-- AlertOutboxStats backs the System page: how many are waiting, and for how long.
-- name: AlertOutboxStats :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::int AS pending,
    coalesce(extract(epoch FROM now() - min(created_at) FILTER (WHERE status = 'pending')), 0)::int AS oldest_pending_age_seconds
FROM alert_outbox;
