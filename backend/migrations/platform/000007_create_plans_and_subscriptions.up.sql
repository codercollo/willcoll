-- Global plan catalog, owned by the platform (not tenant-scoped, no RLS).
-- One catalog for all three products, told apart by `product`; plans are
-- never deleted, only archived, so subscriber/invoice history never
-- dangles (Rentals' migration 000044 pricing overhaul, folded straight
-- into this fresh schema rather than replayed incrementally).
CREATE TABLE IF NOT EXISTS subscription_plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste')),
    name text NOT NULL,
    price numeric(12,2) NOT NULL,
    billing_interval text NOT NULL,
    unit_cap int NULL,
    included_seats int NOT NULL DEFAULT 1,
    features text[] NOT NULL DEFAULT '{}',
    is_test bool NOT NULL DEFAULT false,
    archived_at timestamptz NULL,
    pricing_type text NOT NULL DEFAULT 'flat' CHECK (pricing_type IN ('flat', 'per_unit')),
    per_unit_price numeric(12,2) NULL,
    min_price numeric(12,2) NULL,
    sort_order int NOT NULL DEFAULT 0,
    CONSTRAINT subscription_plans_per_unit_fields CHECK (
        (pricing_type = 'flat' AND per_unit_price IS NULL AND min_price IS NULL)
        OR (pricing_type = 'per_unit' AND per_unit_price IS NOT NULL AND min_price IS NOT NULL)
    )
);

-- Plans are never deleted, so "name" is never globally unique; scope
-- uniqueness to active plans within a product, so an archived plan never
-- blocks a same-named successor and each product can reuse tier names
-- ("Starter") independently.
CREATE UNIQUE INDEX subscription_plans_product_name_active_key
    ON subscription_plans (product, name) WHERE archived_at IS NULL;

-- One active subscription per (manager, product): a manager subscribes to
-- Rentals and Estates independently, each with its own plan/period/status.
CREATE TABLE IF NOT EXISTS subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste')),
    plan_id uuid NOT NULL REFERENCES subscription_plans (id),
    status text NOT NULL CHECK (status IN ('trialing', 'active', 'past_due', 'cancelled')),
    current_period_start date NOT NULL,
    current_period_end date NOT NULL,
    payhero_channel_id text NULL,
    version integer NOT NULL DEFAULT 1
);

CREATE INDEX subscriptions_manager_id_idx ON subscriptions (manager_id);

-- A manager may have historical cancelled subscriptions for a product, but
-- only ever one that is still open (trialing/active/past_due).
CREATE UNIQUE INDEX subscriptions_manager_product_open_key
    ON subscriptions (manager_id, product) WHERE status <> 'cancelled';

CREATE TABLE IF NOT EXISTS subscription_invoices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    subscription_id uuid NOT NULL REFERENCES subscriptions ON DELETE CASCADE,
    -- The plan CHOSEN at renewal (system-design.txt B.7 / Rentals' migration
    -- 000043): may differ from the subscription's current plan if the
    -- manager is switching, and it's this plan the webhook activates.
    plan_id uuid NOT NULL REFERENCES subscription_plans (id),
    amount numeric(12,2) NOT NULL,
    period date NOT NULL,
    mpesa_receipt text UNIQUE NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed')),
    paid_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX subscription_invoices_manager_id_idx ON subscription_invoices (manager_id);
