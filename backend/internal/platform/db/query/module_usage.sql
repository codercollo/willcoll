-- Billable usage, one row per (manager, product) — system-design.txt A.7.
-- Tenant-scoped (RLS by manager_id): every query here runs inside the
-- owning manager's tenant transaction, same as any other tenant-scoped
-- table. There is no cross-manager list query here on purpose — pricing
-- always asks for one manager's one product at a time.

-- name: GetModuleUsage :one
SELECT * FROM module_usage
WHERE manager_id = $1 AND product = $2;

-- UpsertModuleUsage is what a product API calls on a count change (unit/
-- customer created, archived, imported) and from its daily re-count job.
-- name: UpsertModuleUsage :one
INSERT INTO module_usage (manager_id, product, billable_count, reported_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (manager_id, product) DO UPDATE
SET billable_count = EXCLUDED.billable_count, reported_at = EXCLUDED.reported_at
RETURNING *;
