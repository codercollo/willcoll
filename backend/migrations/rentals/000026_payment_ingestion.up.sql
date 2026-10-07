-- Payment ingestion and the manager review queue (system-design.txt 4.8, 5).
--
-- A payment the engine applied on its own best guess (the default waterfall)
-- stays flagged until a manager confirms it; a payment it could not place
-- carries a note saying why. Both show in the review queue.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS auto_applied_unconfirmed boolean NOT NULL DEFAULT false;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS review_note text NULL;

-- The review queue lists what is unmatched, matched-but-unplaced or
-- auto-applied-unconfirmed, newest first.
CREATE INDEX IF NOT EXISTS payments_review_idx ON payments (tenant_id, received_at DESC)
    WHERE status <> 'allocated' OR auto_applied_unconfirmed;

-- An STK push can fail or be cancelled by the customer; keep that visible
-- instead of leaving the intent pending until it expires. The checkout
-- request id PayHero returns lets a status poll be tied to its push.
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_status_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_check
    CHECK (status IN ('pending', 'completed', 'expired', 'failed'));
ALTER TABLE payment_intents ADD COLUMN IF NOT EXISTS checkout_request_id text NULL;
ALTER TABLE payment_intents ADD COLUMN IF NOT EXISTS failure_reason text NULL;
