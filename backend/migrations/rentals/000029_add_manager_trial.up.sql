-- Free trial: a firm gets a fixed number of days (config TRIAL_DAYS, default 7)
-- from the moment it activates, to feel the product before it must subscribe.
-- NULL means the firm has not activated yet, so its trial has not started.
ALTER TABLE managers ADD COLUMN IF NOT EXISTS trial_ends_at timestamptz NULL;

-- Firms that are already active and have never subscribed get a fresh trial
-- rather than being locked out the moment this ships.
UPDATE managers m
SET trial_ends_at = now() + interval '7 days'
WHERE m.activated_at IS NOT NULL
  AND m.trial_ends_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.manager_id = m.id);
