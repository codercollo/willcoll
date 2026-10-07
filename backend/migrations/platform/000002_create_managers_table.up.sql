-- Manager: the SaaS tenant, one login for every product (system-design.txt
-- A.1). trial_ends_at was added after v1.0 (Rentals' migration 000029);
-- carried straight into the platform schema since a manager's trial is a
-- platform concept, not per-product.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS managers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_name text NOT NULL,
    username text UNIQUE NOT NULL,
    email citext UNIQUE NOT NULL,
    phone text NOT NULL,
    password_hash bytea NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'suspended')),
    activated_at timestamptz NULL,
    trial_ends_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);
