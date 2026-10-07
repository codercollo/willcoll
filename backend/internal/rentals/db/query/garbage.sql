-- Garbage billing (system-design.txt 3.9 step 4). No per-unit input: the
-- property's fixed fee is charged to every occupied unit. A garbage_runs
-- row is the run itself (locked on insert); its generated_at is the moment
-- an invoice's "previous balance B/F" is measured against.

-- ListGarbageUnitsForPeriod is every occupied unit with its tenant, current
-- GARBAGE balance, and whether its active lease has garbage billing turned
-- on: the preview grid lists all of these (toggled-off units show "Not
-- billed" rather than disappearing), and a run only charges the billed ones.
-- name: ListGarbageUnitsForPeriod :many
SELECT
    u.id AS unit_id,
    u.unit_code,
    COALESCE(l.tenant_name, '')::text AS tenant_name,
    COALESCE(b.balance, 0)::numeric(12,2) AS prior_balance,
    EXISTS (SELECT 1 FROM leases gl WHERE gl.unit_id = u.id AND gl.status = 'active' AND gl.garbage_billed) AS billed
FROM units u
LEFT JOIN LATERAL (
    SELECT tenant_name FROM leases
    WHERE unit_id = u.id AND status = 'active'
    LIMIT 1
) l ON true
LEFT JOIN LATERAL (
    SELECT ub.balance FROM unit_ledger_balances ub
    WHERE ub.unit_id = u.id AND ub.type = 'GARBAGE'
) b ON true
WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id AND u.status = 'occupied'
ORDER BY u.unit_code;

-- CreateGarbageRun records the run. A second run for the same property and
-- period violates garbage_runs_property_id_period_key.
-- name: CreateGarbageRun :one
INSERT INTO garbage_runs (tenant_id, property_id, period, fee_snapshot, locked)
VALUES ($1, $2, $3, $4, true)
RETURNING id;

-- GarbageRunExists tells the preview whether the period is already billed.
-- name: GarbageRunExists :one
SELECT EXISTS (
    SELECT 1 FROM garbage_runs
    WHERE tenant_id = $1 AND property_id = $2 AND period = $3
);

-- ListGarbageInvoices is the data for every unit billed by the period's
-- run. The fee is the charge actually posted; prior_balance is the GARBAGE
-- balance before the run.
-- name: ListGarbageInvoices :many
SELECT
    ch.ledger_account_id,
    la.unit_id,
    u.unit_code,
    COALESCE(l.tenant_name, '')::text AS tenant_name,
    gr.period,
    gr.generated_at AS bill_date,
    ch.amount AS fee,
    COALESCE((
        SELECT SUM(CASE le.direction WHEN 'DEBIT' THEN le.amount ELSE -le.amount END)
        FROM ledger_entries le
        WHERE le.ledger_account_id = la.id AND le.created_at < gr.generated_at
    ), 0)::numeric(12,2) AS prior_balance
FROM garbage_runs gr
INNER JOIN charges ch ON ch.source_type = 'garbage_run' AND ch.source_id = gr.id
INNER JOIN ledger_accounts la ON la.id = ch.ledger_account_id AND la.type = 'GARBAGE'
INNER JOIN units u ON u.id = la.unit_id
LEFT JOIN LATERAL (
    SELECT tenant_name FROM leases
    WHERE unit_id = u.id AND start_date < (gr.period + interval '1 month')
    ORDER BY start_date DESC
    LIMIT 1
) l ON true
WHERE gr.tenant_id = @tenant_id
  AND gr.property_id = @property_id
  AND gr.period = @period
  AND (sqlc.narg('unit_id')::uuid IS NULL OR la.unit_id = sqlc.narg('unit_id')::uuid)
ORDER BY u.unit_code;
