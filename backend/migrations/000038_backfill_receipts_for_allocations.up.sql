-- Receipts move to one-per-allocation (migration 000035), but allocations
-- posted before that change have no receipt at all: the schedule's
-- "download receipts PDF" comes up empty for any period entirely made of
-- old data. Backfill one receipt per allocation that still has none,
-- oldest allocation first, numbered from the same per-property counter a
-- live posting uses (receipt_counters.next_no).
--
-- arrears_note is left null for backfilled receipts: reconstructing which
-- historical rent payments cleared arrears would require replaying the
-- FIFO ledger as it stood on the day of each payment, which this one-off
-- backfill does not attempt. Every other field matches what issueReceipt
-- (internal/data/allocation.go) would have written at posting time.
WITH missing AS (
    SELECT pa.id AS allocation_id, pa.tenant_id, la.unit_id, u.property_id, la.type AS ledger_type,
           (date_trunc('month', p.received_at AT TIME ZONE 'Africa/Nairobi'))::date AS period,
           pa.created_at
    FROM payment_allocations pa
    JOIN ledger_accounts la ON la.id = pa.ledger_account_id
    JOIN units u ON u.id = la.unit_id
    JOIN payments p ON p.id = pa.payment_id
    WHERE NOT EXISTS (SELECT 1 FROM receipts r WHERE r.payment_allocation_id = pa.id)
),
numbered AS (
    SELECT m.*, row_number() OVER (PARTITION BY property_id ORDER BY created_at, allocation_id) AS rn
    FROM missing m
),
inserted AS (
    INSERT INTO receipts (tenant_id, property_id, unit_id, receipt_no, period, payment_allocation_id, ledger_type)
    SELECT n.tenant_id, n.property_id, n.unit_id, rc.next_no + n.rn - 1, n.period, n.allocation_id, n.ledger_type
    FROM numbered n
    JOIN receipt_counters rc ON rc.property_id = n.property_id
    RETURNING property_id
)
UPDATE receipt_counters rc
SET next_no = rc.next_no + sub.cnt
FROM (SELECT property_id, count(*) AS cnt FROM inserted GROUP BY property_id) sub
WHERE rc.property_id = sub.property_id;
