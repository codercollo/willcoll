-- Pricing overhaul: test-plan flag, archiving (plans are never deleted),
-- flat vs per-unit pricing, and admin display ordering.
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS is_test bool NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS archived_at timestamptz NULL,
    ADD COLUMN IF NOT EXISTS pricing_type text NOT NULL DEFAULT 'flat'
        CHECK (pricing_type IN ('flat', 'per_unit')),
    ADD COLUMN IF NOT EXISTS per_unit_price numeric(12,2) NULL,
    ADD COLUMN IF NOT EXISTS min_price numeric(12,2) NULL,
    ADD COLUMN IF NOT EXISTS sort_order int NOT NULL DEFAULT 0;

-- A per_unit plan prices from per_unit_price/min_price, not price; a flat
-- plan has no use for either.
ALTER TABLE subscription_plans
    ADD CONSTRAINT subscription_plans_per_unit_fields CHECK (
        (pricing_type = 'flat' AND per_unit_price IS NULL AND min_price IS NULL)
        OR (pricing_type = 'per_unit' AND per_unit_price IS NOT NULL AND min_price IS NOT NULL)
    );

UPDATE subscription_plans SET is_test = true WHERE name = 'TEST - KES 10';

-- Archive the superseded plans first (existing subscribers keep their
-- period and can't renew onto them again), which frees their names for the
-- active-only uniqueness check below and for a new plan to reuse "Enterprise".
UPDATE subscription_plans SET archived_at = now()
WHERE name IN ('Professional', 'Enterprise', 'Quarterly Pro') AND archived_at IS NULL;

-- Plans are never deleted, so "name" was never unique; scope uniqueness to
-- active plans only, so an archived plan never blocks a same-named successor.
CREATE UNIQUE INDEX IF NOT EXISTS subscription_plans_name_active_key
    ON subscription_plans (name) WHERE archived_at IS NULL;

-- Idempotent production plan set: insert-or-update by (active) name so
-- re-running this migration never touches subscriber or invoice history.
INSERT INTO subscription_plans (name, price, billing_interval, unit_cap, pricing_type, per_unit_price, min_price, sort_order)
VALUES
    ('Starter', 1500, 'monthly', 20, 'flat', NULL, NULL, 10),
    ('Growth', 3500, 'monthly', 60, 'flat', NULL, NULL, 20),
    ('Business', 7500, 'monthly', 150, 'flat', NULL, NULL, 30),
    ('Enterprise', 7500, 'monthly', NULL, 'per_unit', 45, 7500, 40)
ON CONFLICT (name) WHERE archived_at IS NULL DO UPDATE SET
    price = EXCLUDED.price,
    billing_interval = EXCLUDED.billing_interval,
    unit_cap = EXCLUDED.unit_cap,
    pricing_type = EXCLUDED.pricing_type,
    per_unit_price = EXCLUDED.per_unit_price,
    min_price = EXCLUDED.min_price,
    sort_order = EXCLUDED.sort_order;
