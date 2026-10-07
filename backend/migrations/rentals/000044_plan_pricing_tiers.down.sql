DROP INDEX IF EXISTS subscription_plans_name_active_key;

ALTER TABLE subscription_plans
    DROP CONSTRAINT IF EXISTS subscription_plans_per_unit_fields,
    DROP COLUMN IF EXISTS is_test,
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS pricing_type,
    DROP COLUMN IF EXISTS per_unit_price,
    DROP COLUMN IF EXISTS min_price,
    DROP COLUMN IF EXISTS sort_order;
