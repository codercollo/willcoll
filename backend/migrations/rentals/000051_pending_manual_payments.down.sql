DROP INDEX IF EXISTS payments_pending_idx;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_rejected_has_reason;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_pending_has_unit_and_ledger;
-- Pending and rejected rows have no place in the old status set: a pending one
-- was never posted, so it is simply forgotten; a rejected one likewise.
DELETE FROM payments WHERE status IN ('pending', 'rejected');
ALTER TABLE payments DROP COLUMN IF EXISTS reject_reason;
ALTER TABLE payments DROP COLUMN IF EXISTS rejected_at;
ALTER TABLE payments DROP COLUMN IF EXISTS rejected_by;
ALTER TABLE payments DROP COLUMN IF EXISTS confirmed_at;
ALTER TABLE payments DROP COLUMN IF EXISTS confirmed_by;
ALTER TABLE payments DROP COLUMN IF EXISTS recorded_by_role;
ALTER TABLE payments DROP COLUMN IF EXISTS recorded_by;
ALTER TABLE payments DROP COLUMN IF EXISTS pending_ledger_type;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check CHECK (status IN ('unmatched', 'matched', 'allocated'));
