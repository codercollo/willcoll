CREATE TABLE IF NOT EXISTS units (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    unit_code text NOT NULL,
    meter_number text NULL,
    status text NOT NULL DEFAULT 'vacant' CHECK (status IN ('vacant', 'occupied')),
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1,
    UNIQUE (property_id, unit_code)
);

CREATE INDEX IF NOT EXISTS units_tenant_id_idx ON units (tenant_id);
CREATE INDEX IF NOT EXISTS units_property_id_idx ON units (property_id);
