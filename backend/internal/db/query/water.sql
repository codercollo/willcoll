-- Water billing (system-design.txt 3.9). water_readings rows are drafts
-- (locked = false) until a run locks them; only then are they charged.
-- Balances are never stored: the grid reads the current derived balance and
-- an invoice recomputes "previous balance B/F" from ledger entries created
-- strictly before the reading's locked_at, so it can be re-rendered at any
-- time without a stored PDF.

-- ListWaterGrid is the draft grid: one row per occupied unit, with its
-- saved reading for the period (if any), last period's closing reading as
-- the default "previous", and the unit's current WATER balance.
-- name: ListWaterGrid :many
SELECT
    u.id AS unit_id,
    u.unit_code,
    COALESCE(l.tenant_name, '')::text AS tenant_name,
    wr.id AS reading_id,
    (wr.id IS NOT NULL)::boolean AS has_reading,
    COALESCE(wr.previous_reading, prev.current_reading, 0)::numeric(12,2) AS previous_reading,
    COALESCE(wr.current_reading, 0)::numeric(12,2) AS current_reading,
    COALESCE(wr.units_consumed, 0)::numeric(12,2) AS units_consumed,
    COALESCE(wr.rate_snapshot, 0)::numeric(12,2) AS rate,
    COALESCE(wr.amount, 0)::numeric(12,2) AS amount,
    COALESCE(wr.locked, false)::boolean AS locked,
    COALESCE(b.balance, 0)::numeric(12,2) AS prior_balance
FROM units u
LEFT JOIN LATERAL (
    SELECT tenant_name FROM leases
    WHERE unit_id = u.id AND status = 'active'
    LIMIT 1
) l ON true
LEFT JOIN water_readings wr ON wr.unit_id = u.id AND wr.period = @period
LEFT JOIN LATERAL (
    SELECT p.current_reading FROM water_readings p
    WHERE p.unit_id = u.id AND p.period < @period
    ORDER BY p.period DESC
    LIMIT 1
) prev ON true
LEFT JOIN LATERAL (
    SELECT ub.balance FROM unit_ledger_balances ub
    WHERE ub.unit_id = u.id AND ub.type = 'WATER'
) b ON true
WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id AND u.status = 'occupied'
ORDER BY u.unit_code;

-- UpsertWaterReading saves a draft. The WHERE on the conflict arm makes a
-- locked reading immutable: zero rows (sql.ErrNoRows) means it is locked.
-- Callers verify the unit belongs to the property first.
-- name: UpsertWaterReading :one
INSERT INTO water_readings (
    tenant_id, unit_id, period, previous_reading, current_reading, rate_snapshot, recorded_by, reading_date
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (unit_id, period) DO UPDATE
SET previous_reading = EXCLUDED.previous_reading,
    current_reading = EXCLUDED.current_reading,
    rate_snapshot = EXCLUDED.rate_snapshot,
    recorded_by = EXCLUDED.recorded_by,
    reading_date = EXCLUDED.reading_date
WHERE water_readings.locked = false
RETURNING id;

-- ListWaterReadingsForRun is every reading of the property's period, the
-- input to a run.
-- name: ListWaterReadingsForRun :many
SELECT wr.id, wr.unit_id, u.unit_code, COALESCE(wr.amount, 0)::numeric(12,2) AS amount, wr.locked
FROM water_readings wr
INNER JOIN units u ON u.id = wr.unit_id
WHERE wr.tenant_id = @tenant_id AND u.property_id = @property_id AND wr.period = @period
ORDER BY u.unit_code;

-- LockWaterReading locks a draft as part of a run. Zero rows affected means
-- it was already locked.
-- name: LockWaterReading :execrows
UPDATE water_readings
SET locked = true, locked_at = now()
WHERE tenant_id = $1 AND id = $2 AND locked = false;

-- ListWaterInvoices is the data for every billed unit's water bill in a
-- period. The customer is the lease that started most recently on or before
-- the end of the period; prior_balance is the WATER balance before the run.
-- name: ListWaterInvoices :many
SELECT
    wr.unit_id,
    u.unit_code,
    COALESCE(l.tenant_name, '')::text AS tenant_name,
    wr.period,
    wr.reading_date,
    wr.previous_reading,
    wr.current_reading,
    COALESCE(wr.units_consumed, 0)::numeric(12,2) AS units_consumed,
    wr.rate_snapshot AS rate,
    COALESCE(wr.amount, 0)::numeric(12,2) AS amount,
    COALESCE((
        SELECT SUM(CASE le.direction WHEN 'DEBIT' THEN le.amount ELSE -le.amount END)
        FROM ledger_entries le
        INNER JOIN ledger_accounts la ON la.id = le.ledger_account_id
        WHERE la.tenant_id = @tenant_id
          AND la.unit_id = wr.unit_id
          AND la.type = 'WATER'
          AND le.created_at < wr.locked_at
    ), 0)::numeric(12,2) AS prior_balance
FROM water_readings wr
INNER JOIN units u ON u.id = wr.unit_id
LEFT JOIN LATERAL (
    SELECT tenant_name FROM leases
    WHERE unit_id = wr.unit_id AND start_date < (wr.period + interval '1 month')
    ORDER BY start_date DESC
    LIMIT 1
) l ON true
WHERE wr.tenant_id = @tenant_id
  AND u.property_id = @property_id
  AND wr.period = @period
  AND wr.locked = true
  AND (sqlc.narg('unit_id')::uuid IS NULL OR wr.unit_id = sqlc.narg('unit_id')::uuid)
ORDER BY u.unit_code;
