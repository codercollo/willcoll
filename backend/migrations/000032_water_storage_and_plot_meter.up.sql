-- NOTE:1's storage-capacity lines (system-design.txt PDF confirmation
-- addendum): underground/rooftop capacity are per-property constants;
-- nullable, so a property that hasn't configured them simply omits the
-- lines rather than printing zero. The plot meter is read per period (it
-- measures everything passed through the plot that month, same shape as a
-- unit's water_readings), so it needs its own row, not a properties column.
ALTER TABLE properties ADD COLUMN IF NOT EXISTS underground_capacity_units numeric(12,2) NULL;
ALTER TABLE properties ADD COLUMN IF NOT EXISTS rooftop_capacity_units numeric(12,2) NULL;

CREATE TABLE IF NOT EXISTS property_meter_readings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    period date NOT NULL,
    units_consumed numeric(12,2) NOT NULL,
    recorded_by uuid NOT NULL REFERENCES managers (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (property_id, period)
);

CREATE INDEX IF NOT EXISTS property_meter_readings_tenant_id_idx ON property_meter_readings (tenant_id);

ALTER TABLE property_meter_readings ENABLE ROW LEVEL SECURITY;
ALTER TABLE property_meter_readings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON property_meter_readings
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
