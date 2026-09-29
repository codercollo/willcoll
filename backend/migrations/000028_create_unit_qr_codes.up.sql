-- Lease-bound unit QR codes for tenant payments. A sticker on the unit door
-- encodes ONLY a URL carrying the token: the token identifies the unit, it
-- does not authenticate anyone (the tenant still verifies by SMS on the pay
-- page). A code belongs to a lease and dies with it: it is revoked when the
-- lease ends, or rotated by the manager if the sticker leaks.
--
-- The token is 12 characters from an unambiguous, case-insensitive alphabet
-- (no 0/O/1/I/L), stored upper-case without dashes.
--
-- Tenant-scoped under RLS like every other table (tenant_id is the firm, the
-- manager id). The public /q/:token endpoint has no tenant yet, so it resolves
-- the token through willcoll_admin (SELECT only), then does the write inside
-- that tenant's transaction, exactly like the pay page's unit lookup.
CREATE TABLE IF NOT EXISTS unit_qr_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token text NOT NULL,
    lease_id uuid NOT NULL REFERENCES leases ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    created_by uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    scan_count integer NOT NULL DEFAULT 0,
    last_scanned_at timestamptz NULL,
    CONSTRAINT unit_qr_codes_token_format CHECK (token ~ '^[2-9A-HJKMNP-Z]{10,}$'),
    CONSTRAINT unit_qr_codes_scan_count_nonneg CHECK (scan_count >= 0)
);

-- A token is globally unique, revoked or not: a printed sticker must never
-- start resolving to a different unit.
CREATE UNIQUE INDEX IF NOT EXISTS unit_qr_codes_token_key ON unit_qr_codes (token);

-- One ACTIVE code per lease. Revoked ones stay as history.
CREATE UNIQUE INDEX IF NOT EXISTS unit_qr_codes_one_active_per_lease ON unit_qr_codes (lease_id) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS unit_qr_codes_tenant_lease_idx ON unit_qr_codes (tenant_id, lease_id, created_at DESC);

ALTER TABLE unit_qr_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE unit_qr_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON unit_qr_codes USING (tenant_id = current_setting('app.tenant_id')::uuid);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'willcoll_app') THEN
        -- Codes are never deleted: revoking sets revoked_at.
        GRANT SELECT, INSERT, UPDATE ON unit_qr_codes TO willcoll_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'willcoll_admin') THEN
        GRANT SELECT ON unit_qr_codes TO willcoll_admin;
    END IF;
END $$;
