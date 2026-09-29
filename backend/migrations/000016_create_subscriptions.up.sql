-- Global plan catalog, owned by the platform (not tenant-scoped, no RLS).
CREATE TABLE IF NOT EXISTS subscription_plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    price numeric(12,2) NOT NULL,
    billing_interval text NOT NULL,
    unit_cap int NULL
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    plan_id uuid NOT NULL REFERENCES subscription_plans (id),
    status text NOT NULL CHECK (status IN ('trialing', 'active', 'past_due', 'cancelled')),
    current_period_start date NOT NULL,
    current_period_end date NOT NULL,
    payhero_channel_id text NULL,
    version integer NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS subscriptions_manager_id_idx ON subscriptions (manager_id);

CREATE TABLE IF NOT EXISTS subscription_invoices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    subscription_id uuid NOT NULL REFERENCES subscriptions ON DELETE CASCADE,
    amount numeric(12,2) NOT NULL,
    period date NOT NULL,
    mpesa_receipt text UNIQUE NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed')),
    paid_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS subscription_invoices_manager_id_idx ON subscription_invoices (manager_id);
