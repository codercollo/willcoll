-- Willcoll stores no PDF blobs: documents are rendered on demand from
-- ledger data. Drop the object-key columns, give reports a manager-editable
-- notes list ("NOTE: 2" on the payments schedule), and make a receipt
-- unique per unit and period so regenerating one reuses its number.
ALTER TABLE receipts DROP COLUMN IF EXISTS pdf_object_key;
ALTER TABLE reports DROP COLUMN IF EXISTS pdf_object_key;
ALTER TABLE reports ADD COLUMN IF NOT EXISTS notes jsonb NOT NULL DEFAULT '[]';
CREATE UNIQUE INDEX IF NOT EXISTS receipts_unit_period_key ON receipts (unit_id, period);

-- A bill's "Previous Balance B/F" is recomputed on demand as the ledger
-- balance strictly before the moment the run was locked, so the moment is
-- recorded. (Garbage runs already carry generated_at.)
ALTER TABLE water_readings ADD COLUMN IF NOT EXISTS locked_at timestamptz;
