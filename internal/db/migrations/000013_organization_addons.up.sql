-- organization_addons (spec §3.5, §12.3) — premium add-on gating, billed the
-- same way as the base subscription. Gates the Verified Property Score.

CREATE TYPE addon_kind AS ENUM ('verified_property_score');
CREATE TYPE addon_status AS ENUM ('inactive', 'active', 'past_due', 'cancelled');

CREATE TABLE organization_addons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    addon_key addon_kind NOT NULL,
    status addon_status NOT NULL DEFAULT 'inactive',
    monthly_fee_kes NUMERIC(10,2) NOT NULL, -- flat per-Organization fee, §12.3
    activated_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    UNIQUE (organization_id, addon_key)
);

ALTER TABLE organization_addons ENABLE ROW LEVEL SECURITY;
CREATE POLICY organization_addons_select ON organization_addons FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY organization_addons_insert ON organization_addons FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY organization_addons_update ON organization_addons FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY organization_addons_delete ON organization_addons FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);
