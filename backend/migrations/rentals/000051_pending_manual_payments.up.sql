-- Hand-entered payments by a caretaker wait for confirmation (c4-matrix-fixes.md
-- item 3). An officer typing "cash received" is the classic way arrears get
-- quietly cleared for a friend, so a payment recorded by someone who cannot
-- confirm it is stored as 'pending': NOTHING posts to the ledger, no balance,
-- arrears figure, report or receipt changes, until an accountant (or the owner)
-- confirms it. A reviewer can instead reject it with a reason; a rejected
-- payment never posts and is kept as the record of what was refused.
--
--   * pending_ledger_type: the ledger the payment will be credited to on
--     confirmation (rent, water, ...). The unit is matched_unit_id, set up front.
--   * recorded_by / recorded_by_role: who entered it (a staff id, or the firm
--     login's manager id) and the role they held. Both come from the session,
--     never from the request body. The confirmer must be a different person.
--   * confirmed_by/at, rejected_by/at/reject_reason: who decided, when, and why.
--
-- Postings and their receipts are unchanged: a payment still posts through
-- postPaymentAllocations exactly once (the transaction header's idempotency key
-- is "payment:<id>"), now at confirmation instead of at entry.
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
    CHECK (status IN ('unmatched', 'matched', 'allocated', 'pending', 'rejected'));

ALTER TABLE payments ADD COLUMN IF NOT EXISTS pending_ledger_type text NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS recorded_by uuid NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS recorded_by_role text NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS confirmed_by uuid NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS confirmed_at timestamptz NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS rejected_by uuid NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS rejected_at timestamptz NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS reject_reason text NULL;

-- A pending row carries what it needs to post, and a rejected one says why.
ALTER TABLE payments ADD CONSTRAINT payments_pending_has_unit_and_ledger
    CHECK (status <> 'pending' OR (matched_unit_id IS NOT NULL AND pending_ledger_type IS NOT NULL AND recorded_by IS NOT NULL));
ALTER TABLE payments ADD CONSTRAINT payments_rejected_has_reason
    CHECK (status <> 'rejected' OR (rejected_by IS NOT NULL AND rejected_at IS NOT NULL AND reject_reason IS NOT NULL));

-- The dashboard counts pending payments that have waited too long.
CREATE INDEX IF NOT EXISTS payments_pending_idx ON payments (tenant_id, created_at) WHERE status = 'pending';
