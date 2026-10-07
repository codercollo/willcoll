-- manager_modules: per-manager, per-product entitlement (system-design.txt
-- A.5's requireModule). Tenant-scoped (RLS by manager_id).

-- name: GetManagerModule :one
SELECT * FROM manager_modules
WHERE manager_id = $1 AND product = $2;

-- UpsertManagerModule is used by the backfill tool (build-phases.txt 3d)
-- to seed every existing manager's module row, and later by the Modules
-- page ("start trial" / admin activation) once that exists.
-- name: UpsertManagerModule :one
INSERT INTO manager_modules (manager_id, product, status, activated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (manager_id, product) DO UPDATE
SET status = EXCLUDED.status, activated_at = EXCLUDED.activated_at, updated_at = now()
RETURNING *;

-- StartModuleTrial creates a manager's first module row for product,
-- trialing from trial_ends_at (build-phases.txt 3f-3). ON CONFLICT DO
-- NOTHING: a trial starts once — a second call (e.g. a retried request)
-- must never push the trial's end date out further, so it returns no row,
-- and the caller re-reads the existing one (GetManagerModule) instead of
-- treating that as an error.
-- name: StartModuleTrial :one
INSERT INTO manager_modules (manager_id, product, status, trial_ends_at)
VALUES ($1, $2, 'trialing', $3)
ON CONFLICT (manager_id, product) DO NOTHING
RETURNING *;

-- GetSubscriptionByManagerAndProduct backs the requireSubscription shadow
-- gate (build-phases.txt 3e-b): the same shape as Rentals' own
-- GetSubscriptionByManager, scoped additionally by product now that
-- subscriptions cover every product in one table.
-- name: GetSubscriptionByManagerAndProduct :one
SELECT * FROM subscriptions
WHERE manager_id = $1 AND product = $2
ORDER BY current_period_end DESC
LIMIT 1;

-- SetModuleTrialEnd rewrites a module's trial end. cmd/devtools'
-- dev/expire-trial and dev/reset-trial only (ModuleModel.SetTrialEndForDev,
-- development-only); never called from an API.
-- name: SetModuleTrialEnd :one
UPDATE manager_modules
SET trial_ends_at = @trial_ends_at, updated_at = now()
WHERE manager_id = @manager_id AND product = @product
RETURNING *;
