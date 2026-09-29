-- Rent (system-design.txt 2, 3.3). Expected rent is leases.rent_amount; a
-- rent run copies it into rent_runs.amount_snapshot when a month is billed,
-- and UNIQUE (unit_id, period) is what stops a period being billed twice.

-- GetActiveLeaseByUnit is the current lease of a unit (at most one: the
-- partial unique index on active leases).
-- name: GetActiveLeaseByUnit :one
SELECT * FROM leases
WHERE tenant_id = $1 AND unit_id = $2 AND status = 'active';

-- ListActiveLeasesForProperty is every active lease on the property with its
-- unit: the source of expected rent for the rent schedule and monthly runs.
-- name: ListActiveLeasesForProperty :many
SELECT
    u.id AS unit_id,
    u.unit_code,
    l.id AS lease_id,
    l.tenant_name,
    l.rent_amount,
    l.start_date
FROM leases l
INNER JOIN units u ON u.id = l.unit_id
WHERE l.tenant_id = @tenant_id
  AND u.property_id = @property_id
  AND l.status = 'active'
ORDER BY u.unit_code;

-- UpdateLeaseRentAmount sets the expected monthly rent on an active lease.
-- It posts nothing: rent runs copy this amount when a month is billed, so
-- already-billed periods are unaffected. Zero rows means the lease is not
-- active (or is gone).
-- name: UpdateLeaseRentAmount :one
UPDATE leases
SET rent_amount = @rent_amount,
    version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND status = 'active'
RETURNING version;

-- InsertRentRun records the run, already locked: rent has no draft step.
-- Zero rows (sql.ErrNoRows) means the unit is already billed for the period.
-- name: InsertRentRun :one
INSERT INTO rent_runs (tenant_id, unit_id, period, amount_snapshot, locked)
VALUES ($1, $2, $3, $4, true)
ON CONFLICT (unit_id, period) DO NOTHING
RETURNING id;

-- name: GetRentRun :one
SELECT * FROM rent_runs
WHERE tenant_id = $1 AND unit_id = $2 AND period = $3;

-- name: ListRentRunsForProperty :many
SELECT rr.*
FROM rent_runs rr
INNER JOIN units u ON u.id = rr.unit_id
WHERE rr.tenant_id = @tenant_id AND u.property_id = @property_id AND rr.period = @period;

-- ListRentOverview is the property Rent tab: one row per occupied unit with
-- an active lease. Expected is the billed snapshot if the period has been
-- run, else the current lease rent. Paid is what was received in the period
-- (Kenya calendar dates) against the RENT ledger, ignoring payments that
-- have since been reversed. Balance is the derived RENT balance.
-- name: ListRentOverview :many
SELECT
    u.id AS unit_id,
    u.unit_code,
    l.tenant_name,
    COALESCE(rr.amount_snapshot, l.rent_amount)::numeric(12,2) AS expected,
    (rr.id IS NOT NULL)::boolean AS billed,
    COALESCE((
        SELECT SUM(pa.amount)
        FROM payment_allocations pa
        INNER JOIN payments p ON p.id = pa.payment_id
        INNER JOIN ledger_accounts la ON la.id = pa.ledger_account_id
        WHERE la.unit_id = u.id
          AND la.type = 'RENT'
          AND (p.received_at AT TIME ZONE 'Africa/Nairobi')::date >= sqlc.arg(period)::date
          AND (p.received_at AT TIME ZONE 'Africa/Nairobi')::date < (sqlc.arg(period)::date + interval '1 month')
          AND NOT EXISTS (
              SELECT 1 FROM ledger_entries r
              WHERE r.reference_type = 'reversal' AND r.reference_id = pa.ledger_entry_id
          )
    ), 0)::numeric(12,2) AS paid,
    COALESCE(b.balance, 0)::numeric(12,2) AS balance
FROM units u
INNER JOIN leases l ON l.unit_id = u.id AND l.status = 'active'
LEFT JOIN rent_runs rr ON rr.unit_id = u.id AND rr.period = sqlc.arg(period)
LEFT JOIN LATERAL (
    SELECT ub.balance FROM unit_ledger_balances ub
    WHERE ub.unit_id = u.id AND ub.type = 'RENT'
) b ON true
WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id AND u.status = 'occupied'
ORDER BY u.unit_code;
