-- Per-product trials (build-phases.txt 3f-3, system-design.txt A.7): a
-- trial belongs to one product's module row, not the manager as a whole —
-- a manager whose Rentals trial has expired still gets a fresh Estates
-- trial when they start Estates. managers.trial_ends_at is left in place
-- (still read by anything not yet updated) and dropped in a later
-- migration once nothing reads it.
ALTER TABLE manager_modules ADD COLUMN trial_ends_at timestamptz NULL;

-- Backfill: every existing manager's trial (managers.trial_ends_at) is
-- copied onto their "rentals" module row, so nobody's current trial
-- changes the moment this ships.
UPDATE manager_modules
SET trial_ends_at = managers.trial_ends_at
FROM managers
WHERE manager_modules.manager_id = managers.id
  AND manager_modules.product = 'rentals'
  AND managers.trial_ends_at IS NOT NULL;
