ALTER TABLE alert_outbox DROP CONSTRAINT IF EXISTS alert_outbox_status_check;
UPDATE alert_outbox SET status = 'pending' WHERE status = 'dead';
ALTER TABLE alert_outbox ADD CONSTRAINT alert_outbox_status_check CHECK (status IN ('pending', 'relayed'));
ALTER TABLE alert_outbox DROP COLUMN IF EXISTS next_attempt_at;
