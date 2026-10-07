-- One-time codes that gate the public pay page (system-design.txt 1.4, 4.7).
-- Only a keyed hash of the code is stored, never the code. Tenant-scoped under
-- RLS like every other table: the public endpoints resolve the manager from
-- the pay URL first, then work inside that tenant's transaction.
CREATE TABLE IF NOT EXISTS pay_otps (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    phone text NOT NULL,
    code_hash text NOT NULL,
    attempts int NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS pay_otps_lookup_idx ON pay_otps (tenant_id, unit_id, phone, created_at DESC);

ALTER TABLE pay_otps ENABLE ROW LEVEL SECURITY;
ALTER TABLE pay_otps FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pay_otps USING (tenant_id = current_setting('app.tenant_id')::uuid);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'willcoll_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON pay_otps TO willcoll_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'willcoll_admin') THEN
        GRANT SELECT ON pay_otps TO willcoll_admin;
    END IF;
END $$;
