-- An alert the relay cannot hand to the platform after 10 attempts is 'dead':
-- no further retries, counted on the System page. Attempts are spaced by
-- next_attempt_at (the same backoff as the platform outbox), so a platform
-- outage of a few minutes cannot burn the budget.
ALTER TABLE alert_outbox ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE alert_outbox DROP CONSTRAINT IF EXISTS alert_outbox_status_check;
ALTER TABLE alert_outbox ADD CONSTRAINT alert_outbox_status_check CHECK (status IN ('pending', 'relayed', 'dead'));
