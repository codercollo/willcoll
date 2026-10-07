ALTER TABLE payment_intents DROP COLUMN IF EXISTS failure_reason;
ALTER TABLE payment_intents DROP COLUMN IF EXISTS checkout_request_id;
UPDATE payment_intents SET status = 'expired' WHERE status = 'failed';
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_status_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_check
    CHECK (status IN ('pending', 'completed', 'expired'));
DROP INDEX IF EXISTS payments_review_idx;
ALTER TABLE payments DROP COLUMN IF EXISTS review_note;
ALTER TABLE payments DROP COLUMN IF EXISTS auto_applied_unconfirmed;
