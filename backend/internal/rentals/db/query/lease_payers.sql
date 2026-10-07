-- Lease co-payers (system-design.txt 2, 1.4): the other people allowed to pay
-- for a unit, the "OR" names on the payments schedule. Their phones can also
-- request the pay-page one-time code and identify organic paybill payments.
-- Tenant-scoped under RLS. Not ledger tables, so rows may be removed.

-- CreateLeasePayer adds a co-payer to a lease.
-- name: CreateLeasePayer :one
INSERT INTO lease_payers (tenant_id, lease_id, name, phone)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListLeasePayers :many
SELECT * FROM lease_payers
WHERE tenant_id = $1 AND lease_id = $2
ORDER BY name, id;

-- DeleteLeasePayer removes a co-payer. Zero rows means it is not on that lease.
-- name: DeleteLeasePayer :execrows
DELETE FROM lease_payers
WHERE tenant_id = $1 AND lease_id = $2 AND id = $3;

-- ListLeasesForUnit is a unit's lease history, newest first: the active lease
-- (at most one) and every terminated one.
-- name: ListLeasesForUnit :many
SELECT * FROM leases
WHERE tenant_id = $1 AND unit_id = $2
ORDER BY start_date DESC, created_at DESC;
