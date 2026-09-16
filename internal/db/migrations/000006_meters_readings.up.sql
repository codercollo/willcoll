-- Phase 6.1: meters and meter readings (spec §3.3).
-- meters inherits organization_id via unit_id; meter_readings inherits it via
-- meter_id -> unit_id. Both RLS policies resolve it transitively.

CREATE TYPE meter_kind AS ENUM ('water');

CREATE TABLE meters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    unit_id UUID NOT NULL REFERENCES units(id) ON DELETE CASCADE,
    meter_type meter_kind NOT NULL,
    meter_number TEXT
);

CREATE TABLE meter_readings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meter_id UUID NOT NULL REFERENCES meters(id) ON DELETE CASCADE,
    reading_month DATE NOT NULL,
    previous_reading NUMERIC(10,2) NOT NULL,
    current_reading NUMERIC(10,2) NOT NULL,
    units_consumed NUMERIC(10,2) GENERATED ALWAYS AS (current_reading - previous_reading) STORED,
    rate_per_m3 NUMERIC(10,2) NOT NULL,
    amount_due NUMERIC(10,2) GENERATED ALWAYS AS ((current_reading - previous_reading) * rate_per_m3) STORED,
    recorded_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (meter_id, reading_month)
);

-- RLS on meters (organization_id inherited via unit_id).
ALTER TABLE meters ENABLE ROW LEVEL SECURITY;
CREATE POLICY meters_select ON meters FOR SELECT USING (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY meters_insert ON meters FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY meters_update ON meters FOR UPDATE USING (
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
CREATE POLICY meters_delete ON meters FOR DELETE USING (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);

-- RLS on meter_readings (organization_id inherited via meter_id -> unit_id).
ALTER TABLE meter_readings ENABLE ROW LEVEL SECURITY;
CREATE POLICY meter_readings_select ON meter_readings FOR SELECT USING (
    EXISTS (
        SELECT 1
        FROM meters m
        JOIN units u ON u.id = m.unit_id
        JOIN properties p ON p.id = u.property_id
        WHERE m.id = meter_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY meter_readings_insert ON meter_readings FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1
        FROM meters m
        JOIN units u ON u.id = m.unit_id
        JOIN properties p ON p.id = u.property_id
        WHERE m.id = meter_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY meter_readings_update ON meter_readings FOR UPDATE USING (
    EXISTS (
        SELECT 1
        FROM meters m
        JOIN units u ON u.id = m.unit_id
        JOIN properties p ON p.id = u.property_id
        WHERE m.id = meter_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
) WITH CHECK (
    EXISTS (
        SELECT 1
        FROM meters m
        JOIN units u ON u.id = m.unit_id
        JOIN properties p ON p.id = u.property_id
        WHERE m.id = meter_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY meter_readings_delete ON meter_readings FOR DELETE USING (
    EXISTS (
        SELECT 1
        FROM meters m
        JOIN units u ON u.id = m.unit_id
        JOIN properties p ON p.id = u.property_id
        WHERE m.id = meter_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
