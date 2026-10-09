-- A queued owner alert that exhausts its delivery attempts is 'dead': no
-- further retries, counted on the System page. This replaces the earlier
-- terminal status 'failed' (migration 000022) and raises the budget to 10
-- attempts (data.OutboxMaxAttempts).
ALTER TABLE email_outbox DROP CONSTRAINT IF EXISTS email_outbox_status_check;
UPDATE email_outbox SET status = 'dead' WHERE status = 'failed';
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_status_check CHECK (status IN ('pending', 'sent', 'dead'));
