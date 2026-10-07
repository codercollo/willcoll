DROP INDEX IF EXISTS receipts_unit_period_key;
ALTER TABLE reports DROP COLUMN IF EXISTS notes;
ALTER TABLE reports ADD COLUMN IF NOT EXISTS pdf_object_key text NOT NULL DEFAULT '';
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS pdf_object_key text NOT NULL DEFAULT '';
ALTER TABLE water_readings DROP COLUMN IF EXISTS locked_at;
