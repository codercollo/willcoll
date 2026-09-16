-- Phase 4.5 prerequisite: tenants and leases (spec §3.2).
-- tenants carries organization_id directly; leases inherits it via unit_id.

CREATE TYPE lease_status AS ENUM ('active', 'terminated', 'notice_period');

CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    full_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    id_number TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE leases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    unit_id UUID NOT NULL REFERENCES units(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monthly_rent NUMERIC(10,2) NOT NULL,
    deposit_paid NUMERIC(10,2) NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE,
    rent_due_day SMALLINT NOT NULL,
    late_fee_pct_per_day NUMERIC(5,2) NOT NULL,
    status lease_status NOT NULL DEFAULT 'active',
    terminated_reason TEXT,
    terminated_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one active lease per unit at a time.
CREATE UNIQUE INDEX leases_one_active_per_unit ON leases (unit_id) WHERE status = 'active';

-- RLS on tenants (direct organization_id column).
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenants_select ON tenants FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tenants_insert ON tenants FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tenants_update ON tenants FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tenants_delete ON tenants FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

-- RLS on leases (organization_id inherited via unit_id).
ALTER TABLE leases ENABLE ROW LEVEL SECURITY;
CREATE POLICY leases_select ON leases FOR SELECT USING (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY leases_insert ON leases FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY leases_update ON leases FOR UPDATE USING (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
) WITH CHECK (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY leases_delete ON leases FOR DELETE USING (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
