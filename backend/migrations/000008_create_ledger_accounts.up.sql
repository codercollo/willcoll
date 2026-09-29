-- One anchor row per unit per ledger type. Holds no balance/amount column;
-- it exists only so ledger_entries has a stable, typed foreign key to
-- aggregate against (system-design.txt 3.3).
CREATE TABLE IF NOT EXISTS ledger_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    type text NOT NULL CHECK (type IN ('RENT', 'WATER', 'GARBAGE', 'RENT_DEPOSIT', 'WATER_DEPOSIT')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (unit_id, type)
);

CREATE INDEX IF NOT EXISTS ledger_accounts_tenant_id_idx ON ledger_accounts (tenant_id);
