-- Receipts move from "one per unit per period, aggregating every ledger
-- type" to "one per payment allocation": a rent+water M-Pesa payment now
-- issues two receipts, one per type, matching the KIWI PLACE carbon book.
-- Numbering moves from a per-tenant MAX(receipt_no)+1 under an advisory
-- lock to a per-property counter row, incremented with SELECT ... FOR
-- UPDATE inside the same transaction that posts the allocation — so a
-- rolled-back posting also rolls back the counter increment, and the
-- sequence stays gap-free.

CREATE TABLE IF NOT EXISTS receipt_counters (
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid PRIMARY KEY REFERENCES properties ON DELETE CASCADE,
    next_no bigint NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS receipt_counters_tenant_id_idx ON receipt_counters (tenant_id);

ALTER TABLE receipt_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE receipt_counters FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON receipt_counters
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- Seed one counter per existing property, continuing from its highest
-- receipt number so far (old numbers were unique per tenant via
-- UNIQUE(tenant_id, receipt_no) — a strictly stronger guarantee than the
-- new UNIQUE(tenant_id, property_id, receipt_no) below, so re-scoping is
-- safe by construction).
INSERT INTO receipt_counters (tenant_id, property_id, next_no)
SELECT p.tenant_id, p.id,
       COALESCE((SELECT MAX(r.receipt_no) FROM receipts r INNER JOIN units u ON u.id = r.unit_id WHERE u.property_id = p.id), 0) + 1
FROM properties p
ON CONFLICT (property_id) DO NOTHING;

-- The old per-unit-per-period uniqueness no longer holds: a unit can now
-- have several receipts in one period (rent, water, garbage, a deposit).
DROP INDEX IF EXISTS receipts_unit_period_key;

-- payment_ids (plural, an array) belonged to the old aggregate-per-period
-- receipt; a new receipt is one allocation, so it becomes optional and a
-- new payment_allocation_id takes its place. Old rows are untouched.
ALTER TABLE receipts ALTER COLUMN payment_ids DROP NOT NULL;
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS property_id uuid REFERENCES properties;
UPDATE receipts SET property_id = (SELECT u.property_id FROM units u WHERE u.id = receipts.unit_id) WHERE property_id IS NULL;
ALTER TABLE receipts ALTER COLUMN property_id SET NOT NULL;

ALTER TABLE receipts ADD COLUMN IF NOT EXISTS payment_allocation_id uuid UNIQUE REFERENCES payment_allocations;
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS ledger_type text
    CHECK (ledger_type IN ('RENT', 'WATER', 'GARBAGE', 'RENT_DEPOSIT', 'WATER_DEPOSIT'));
-- arrears_note: filled only when a RENT allocation clears money owed from
-- before this period ("Arrears" line on the KIWI PLACE receipt).
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS arrears_note text;
-- A reversed allocation voids its receipt rather than deleting it or
-- reusing its number: the paper carbon book never tears a page out.
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS voided_at timestamptz;
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS void_reason text;

ALTER TABLE receipts ADD CONSTRAINT receipts_tenant_property_receipt_no_key UNIQUE (tenant_id, property_id, receipt_no);
CREATE INDEX IF NOT EXISTS receipts_property_id_idx ON receipts (property_id);
