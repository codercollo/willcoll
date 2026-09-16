-- Phase 4.1: properties, units, property_ownership, agent_property_grants.
-- units, property_ownership, and agent_property_grants inherit organization_id
-- transitively through property_id (spec §3.2); their RLS policies resolve it
-- via a subquery on properties.

CREATE TYPE unit_status AS ENUM ('vacant', 'occupied', 'notice_given');

CREATE TABLE properties (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    manager_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    location TEXT NOT NULL,
    brand_note TEXT,
    water_rate_per_m3 NUMERIC(10,2) NOT NULL DEFAULT 200.00,
    garbage_fee_flat NUMERIC(10,2) NOT NULL DEFAULT 300.00,
    late_fee_pct_per_day NUMERIC(5,2) NOT NULL DEFAULT 1.00,
    rent_due_day SMALLINT NOT NULL DEFAULT 5,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE units (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE CASCADE,
    unit_label TEXT NOT NULL,
    unit_type TEXT,
    base_rent NUMERIC(10,2) NOT NULL,
    deposit_amount NUMERIC(10,2) NOT NULL,
    status unit_status NOT NULL DEFAULT 'vacant',
    UNIQUE (property_id, unit_label)
);

CREATE TABLE property_ownership (
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE CASCADE,
    landlord_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ownership_pct NUMERIC(5,2) NOT NULL DEFAULT 100.00,
    PRIMARY KEY (property_id, landlord_id)
);

CREATE TABLE agent_property_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE CASCADE,
    can_record_payments BOOLEAN NOT NULL DEFAULT false,
    can_edit_leases BOOLEAN NOT NULL DEFAULT false,
    can_edit_unit_pricing BOOLEAN NOT NULL DEFAULT false,
    can_void_payments BOOLEAN NOT NULL DEFAULT false,
    can_view_financial_reports BOOLEAN NOT NULL DEFAULT false,
    can_manage_meter_readings BOOLEAN NOT NULL DEFAULT false,
    granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

-- RLS on properties (direct organization_id column).
ALTER TABLE properties ENABLE ROW LEVEL SECURITY;
CREATE POLICY properties_select ON properties FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY properties_insert ON properties FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY properties_update ON properties FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY properties_delete ON properties FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

-- RLS on units (organization_id inherited via property_id).
ALTER TABLE units ENABLE ROW LEVEL SECURITY;
CREATE POLICY units_select ON units FOR SELECT USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY units_insert ON units FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY units_update ON units FOR UPDATE USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
) WITH CHECK (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY units_delete ON units FOR DELETE USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);

-- RLS on property_ownership (organization_id inherited via property_id).
ALTER TABLE property_ownership ENABLE ROW LEVEL SECURITY;
CREATE POLICY property_ownership_select ON property_ownership FOR SELECT USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY property_ownership_insert ON property_ownership FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY property_ownership_update ON property_ownership FOR UPDATE USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
) WITH CHECK (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY property_ownership_delete ON property_ownership FOR DELETE USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);

-- RLS on agent_property_grants (organization_id inherited via property_id).
ALTER TABLE agent_property_grants ENABLE ROW LEVEL SECURITY;
CREATE POLICY agent_property_grants_select ON agent_property_grants FOR SELECT USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY agent_property_grants_insert ON agent_property_grants FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY agent_property_grants_update ON agent_property_grants FOR UPDATE USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
) WITH CHECK (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY agent_property_grants_delete ON agent_property_grants FOR DELETE USING (
    EXISTS (
        SELECT 1 FROM properties p
        WHERE p.id = property_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);


