-- Monthly reports and receipts (system-design.txt 4.6, 4.10). Documents are
-- rendered on demand from the ledger; nothing here stores a PDF. Payment
-- dates are calendar dates in Kenya (Africa/Nairobi), not UTC, so a payment
-- made late on the 30th belongs to that month.

-- ListScheduleUnits is every unit of the property, with the tenant of the
-- lease that started most recently on or before the end of the period and
-- that lease's co-payers (newline-separated, since arrays would need
-- lib/pq in the generated code).
-- name: ListScheduleUnits :many
SELECT
    u.id AS unit_id,
    u.unit_code,
    u.status,
    COALESCE(l.tenant_name, '')::text AS tenant_name,
    COALESCE(l.payer_names, '')::text AS payer_names,
    COALESCE(l.garbage_billed, false) AS garbage_billed
FROM units u
LEFT JOIN LATERAL (
    SELECT
        le.tenant_name,
        le.garbage_billed,
        (SELECT string_agg(x.name, chr(10)) FROM (SELECT lp.name FROM lease_payers lp WHERE lp.lease_id = le.id ORDER BY lp.name) x) AS payer_names
    FROM leases le
    WHERE le.unit_id = u.id AND le.start_date < (sqlc.arg(period)::date + interval '1 month')
    ORDER BY le.start_date DESC
    LIMIT 1
) l ON true
WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id
ORDER BY u.unit_code;

-- ListPeriodPayments is every allocation of a payment received in the
-- period against a unit of the property: one line per allocation.
-- name: ListPeriodPayments :many
SELECT
    la.unit_id,
    la.type AS ledger_type,
    (p.received_at AT TIME ZONE 'Africa/Nairobi')::date AS paid_on,
    pa.amount,
    p.id AS payment_id,
    p.source
FROM payment_allocations pa
INNER JOIN payments p ON p.id = pa.payment_id
INNER JOIN ledger_accounts la ON la.id = pa.ledger_account_id
INNER JOIN units u ON u.id = la.unit_id
WHERE pa.tenant_id = @tenant_id
  AND u.property_id = @property_id
  AND (p.received_at AT TIME ZONE 'Africa/Nairobi')::date >= sqlc.arg(period)::date
  AND (p.received_at AT TIME ZONE 'Africa/Nairobi')::date < (sqlc.arg(period)::date + interval '1 month')
  AND NOT EXISTS (
      SELECT 1 FROM ledger_entries r
      WHERE r.reference_type = 'reversal' AND r.reference_id = pa.ledger_entry_id
  )
ORDER BY u.unit_code, paid_on, p.received_at, pa.created_at;

-- GetWaterSummary totals the period's locked readings. Expected is the
-- consumed units at the property's current rate; billed is what was actually
-- charged (readings can carry a per-run rate), so the difference is the
-- "deviation" on the schedule.
-- name: GetWaterSummary :one
SELECT
    COALESCE(SUM(wr.units_consumed), 0)::numeric(12,2) AS units_consumed,
    (COALESCE(SUM(wr.units_consumed), 0) * @rate::numeric(12,2))::numeric(12,2) AS expected,
    COALESCE(SUM(wr.amount), 0)::numeric(12,2) AS billed
FROM water_readings wr
INNER JOIN units u ON u.id = wr.unit_id
WHERE wr.tenant_id = @tenant_id
  AND u.property_id = @property_id
  AND wr.period = @period
  AND wr.locked = true;

-- ListUnitRentBalancesAsOf gives each unit's rent-account balance using only
-- entries posted before the cutoff (exclusive). Called twice by the NOTE:2
-- arrears/advance derivation — once with the previous period's end as
-- cutoff, once with this period's end — so the caller can diff the two
-- balances into "cleared" / "carried" / "new" arrears, and FIFO-derive
-- advance months from a positive balance at the later cutoff. A positive
-- balance is money owed (arrears); a negative balance is a credit (advance).
-- name: ListUnitRentBalancesAsOf :many
SELECT
    la.unit_id,
    COALESCE(SUM(
        CASE
            WHEN le.direction = 'DEBIT' THEN le.amount
            WHEN le.direction = 'CREDIT' THEN -le.amount
        END
    ), 0)::numeric(12,2) AS balance
FROM ledger_accounts la
INNER JOIN units u ON u.id = la.unit_id
LEFT JOIN ledger_entries le ON le.ledger_account_id = la.id AND le.created_at < sqlc.arg(as_of)::timestamptz
WHERE la.tenant_id = @tenant_id AND u.property_id = @property_id AND la.type = 'RENT'
GROUP BY la.unit_id;

-- ListUnitLeaseRentHistory gives every lease a unit has had (any status),
-- so the caller can look up which rent_amount was in force for a given
-- past month rather than assuming today's rent applied throughout.
-- name: ListUnitLeaseRentHistory :many
SELECT unit_id, rent_amount, start_date, end_date
FROM leases
WHERE tenant_id = @tenant_id AND unit_id = ANY(sqlc.arg(unit_ids)::uuid[])
ORDER BY unit_id, start_date;

-- GetPropertyMeterReading is the period's plot-meter units used, for
-- NOTE:1's storage-deviation line. No row, or a row with no previous
-- reading yet, means units used is unknown; the line is omitted, never
-- printed as zero.
-- name: GetPropertyMeterReading :one
SELECT units_consumed FROM property_meter_readings
WHERE tenant_id = @tenant_id AND property_id = @property_id AND period = @period
  AND units_consumed IS NOT NULL;

-- GetPlotMeterReadingRow is the period's own saved plot-meter row, if any.
-- No row (sql.ErrNoRows) means the period hasn't been read yet.
-- name: GetPlotMeterReadingRow :one
SELECT previous_reading, current_reading, units_consumed, reading_date
FROM property_meter_readings
WHERE tenant_id = @tenant_id AND property_id = @property_id AND period = @period;

-- GetPreviousPropertyMeterReading is the last period before this one that
-- was read, for the reading form's "Previous: X" default (same shape as
-- ListWaterGrid's per-unit default). No row (sql.ErrNoRows) means the
-- property has never been read before — Previous stays editable.
-- name: GetPreviousPropertyMeterReading :one
SELECT current_reading FROM property_meter_readings
WHERE tenant_id = @tenant_id AND property_id = @property_id AND period < @period
ORDER BY period DESC
LIMIT 1;

-- name: UpsertPropertyMeterReading :exec
INSERT INTO property_meter_readings (tenant_id, property_id, period, previous_reading, current_reading, recorded_by, reading_date)
VALUES (@tenant_id, @property_id, @period, @previous_reading, @current_reading, @recorded_by, @reading_date)
ON CONFLICT (property_id, period) DO UPDATE
SET previous_reading = EXCLUDED.previous_reading, current_reading = EXCLUDED.current_reading,
    recorded_by = EXCLUDED.recorded_by, reading_date = EXCLUDED.reading_date;

-- GetReport reads the period's report row (its manager-typed notes).
-- name: GetReport :one
SELECT id, totals, notes, generated_at
FROM reports
WHERE tenant_id = $1 AND property_id = $2 AND period = $3;

-- UpsertReportNotes saves the period's NOTE: 2 lines, creating the row
-- (with empty totals) if the report has not been generated yet.
-- name: UpsertReportNotes :exec
INSERT INTO reports (tenant_id, property_id, period, totals, notes, generated_by)
VALUES ($1, $2, $3, '{}'::jsonb, $4, $5)
ON CONFLICT (property_id, period) DO UPDATE
SET notes = EXCLUDED.notes;

-- UpsertReportTotals records the totals snapshot when a report is
-- generated, keeping any notes already saved.
-- name: UpsertReportTotals :exec
INSERT INTO reports (tenant_id, property_id, period, totals, notes, generated_by)
VALUES ($1, $2, $3, $4, '[]'::jsonb, $5)
ON CONFLICT (property_id, period) DO UPDATE
SET totals = EXCLUDED.totals,
    generated_by = EXCLUDED.generated_by,
    generated_at = now();

-- CreateReceiptCounter is called once, when a property is created (not
-- lazily), so every property always has a counter row before its first
-- payment.
-- name: CreateReceiptCounter :exec
INSERT INTO receipt_counters (tenant_id, property_id, next_no)
VALUES (@tenant_id, @property_id, 1);

-- LockReceiptCounter takes the property's counter row lock for the rest of
-- the transaction (SELECT ... FOR UPDATE, not MAX()+1), so two concurrent
-- postings for the same property can never read the same next_no.
-- name: LockReceiptCounter :one
SELECT next_no FROM receipt_counters
WHERE tenant_id = @tenant_id AND property_id = @property_id
FOR UPDATE;

-- AdvanceReceiptCounter hands out the locked next_no and increments it.
-- Call after LockReceiptCounter, in the same transaction.
-- name: AdvanceReceiptCounter :exec
UPDATE receipt_counters SET next_no = next_no + 1
WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- CreateReceipt issues one allocation's receipt. payment_allocation_id is
-- unique, so a retried posting that (however it happened) reused the same
-- allocation id conflicts here instead of consuming a second number —
-- callers check for that conflict and reuse GetReceiptByAllocation's row
-- instead of treating it as an error.
-- name: CreateReceipt :one
INSERT INTO receipts (tenant_id, property_id, unit_id, receipt_no, period, payment_allocation_id, ledger_type, arrears_note, settled_period)
VALUES (@tenant_id, @property_id, @unit_id, @receipt_no, @period, @payment_allocation_id, @ledger_type, @arrears_note, @settled_period)
RETURNING *;

-- GetReceiptByAllocation is the idempotency read: a retried posting that
-- names the same allocation gets this row back instead of a new receipt.
-- name: GetReceiptByAllocation :one
SELECT * FROM receipts WHERE tenant_id = @tenant_id AND payment_allocation_id = @payment_allocation_id;

-- VoidReceipt marks a receipt void when its allocation is reversed. The
-- number is never reused and the row is never deleted — the PDF renders
-- "VOID" over it instead.
-- name: VoidReceipt :exec
UPDATE receipts SET voided_at = now(), void_reason = @void_reason
WHERE tenant_id = @tenant_id AND payment_allocation_id = @payment_allocation_id;

-- ListReceiptsForPeriod is every (non-void, unless included) receipt for a
-- property's period, for the schedule's "generate receipts" PDF — a read
-- now, since receipts are issued at posting time, not batch-generated.
-- name: ListReceiptsForPeriod :many
SELECT r.*, u.unit_code, COALESCE(l.tenant_name, '')::text AS tenant_name, COALESCE(l.payer_names, '')::text AS payer_names,
    p.mpesa_receipt, p.source, p.received_at, pa.amount
FROM receipts r
INNER JOIN units u ON u.id = r.unit_id
INNER JOIN payment_allocations pa ON pa.id = r.payment_allocation_id
INNER JOIN payments p ON p.id = pa.payment_id
LEFT JOIN LATERAL (
    SELECT
        le.tenant_name,
        (SELECT string_agg(x.name, chr(10)) FROM (SELECT lp.name FROM lease_payers lp WHERE lp.lease_id = le.id ORDER BY lp.name) x) AS payer_names
    FROM leases le
    WHERE le.unit_id = u.id AND le.start_date <= r.period
    ORDER BY le.start_date DESC
    LIMIT 1
) l ON true
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.period = @period
  AND (sqlc.narg('unit_id')::uuid IS NULL OR r.unit_id = sqlc.narg('unit_id')::uuid)
ORDER BY u.unit_code, r.receipt_no;
