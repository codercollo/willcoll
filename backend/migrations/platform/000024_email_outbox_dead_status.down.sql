ALTER TABLE email_outbox DROP CONSTRAINT IF EXISTS email_outbox_status_check;
UPDATE email_outbox SET status = 'failed' WHERE status = 'dead';
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_status_check CHECK (status IN ('pending', 'sent', 'failed'));
