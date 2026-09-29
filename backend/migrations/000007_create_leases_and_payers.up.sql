CREATE TABLE IF NOT EXISTS leases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    tenant_name text NOT NULL,
    primary_phone text NOT NULL,
    rent_amount numeric(12,2) NOT NULL,
    rent_deposit_amount numeric(12,2) NOT NULL DEFAULT 0,
    water_deposit_amount numeric(12,2) NOT NULL DEFAULT 0,
    start_date date NOT NULL,
    end_date date NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'terminated')),
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS leases_tenant_id_idx ON leases (tenant_id);
CREATE INDEX IF NOT EXISTS leases_unit_id_idx ON leases (unit_id);

-- at most one 'active' lease per unit
CREATE UNIQUE INDEX IF NOT EXISTS one_active_lease_per_unit
    ON leases (unit_id) WHERE status = 'active';

-- the "OR NAME OR NAME" co-payer pattern seen in the real report
CREATE TABLE IF NOT EXISTS lease_payers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    lease_id uuid NOT NULL REFERENCES leases ON DELETE CASCADE,
    name text NOT NULL,
    phone text NULL
);

CREATE INDEX IF NOT EXISTS lease_payers_tenant_id_idx ON lease_payers (tenant_id);
CREATE INDEX IF NOT EXISTS lease_payers_lease_id_idx ON lease_payers (lease_id);
