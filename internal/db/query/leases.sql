-- name: GetLease :one
SELECT * FROM leases WHERE id = $1;

-- name: ListLeasesByUnit :many
SELECT * FROM leases WHERE unit_id = $1 ORDER BY start_date;

-- name: CreateLease :one
INSERT INTO leases (
    unit_id, tenant_id, monthly_rent, deposit_paid, start_date, end_date,
    rent_due_day, late_fee_pct_per_day, status, terminated_reason, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateLease :one
UPDATE leases SET
    monthly_rent = $2,
    deposit_paid = $3,
    start_date = $4,
    end_date = $5,
    rent_due_day = $6,
    late_fee_pct_per_day = $7,
    status = $8,
    terminated_reason = $9,
    terminated_at = $10
WHERE id = $1
RETURNING *;

-- name: DeleteLease :exec
DELETE FROM leases WHERE id = $1;
