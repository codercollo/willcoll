CREATE TABLE IF NOT EXISTS properties (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    landlord_id uuid NOT NULL REFERENCES landlords ON DELETE RESTRICT,
    name text NOT NULL,
    location text NOT NULL,
    slug text UNIQUE NOT NULL,
    garbage_enabled boolean NOT NULL DEFAULT false,
    garbage_fee numeric(12,2) NOT NULL DEFAULT 0,
    water_rate_per_unit numeric(12,2) NOT NULL DEFAULT 0,
    management_fee_percent numeric(5,2) NOT NULL DEFAULT 0,
    payhero_channel_id text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS properties_tenant_id_idx ON properties (tenant_id);
CREATE INDEX IF NOT EXISTS properties_landlord_id_idx ON properties (landlord_id);
