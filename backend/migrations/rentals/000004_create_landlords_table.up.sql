CREATE TABLE IF NOT EXISTS landlords (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    name text NOT NULL,
    phone text NOT NULL,
    email text NULL,
    bank_name text NULL,
    bank_account_name text NULL,
    bank_account_number text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS landlords_tenant_id_idx ON landlords (tenant_id);
