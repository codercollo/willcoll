-- The digitized meter-book page. Note: a generated column cannot reference
-- another generated column in PostgreSQL, so `amount` repeats the
-- units_consumed expression rather than referencing that column.
CREATE TABLE IF NOT EXISTS water_readings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    period date NOT NULL,
    previous_reading numeric(12,2) NOT NULL,
    current_reading numeric(12,2) NOT NULL,
    units_consumed numeric(12,2) GENERATED ALWAYS AS (current_reading - previous_reading) STORED,
    rate_snapshot numeric(12,2) NOT NULL,
    amount numeric(12,2) GENERATED ALWAYS AS ((current_reading - previous_reading) * rate_snapshot) STORED,
    locked boolean NOT NULL DEFAULT false,
    recorded_by uuid NOT NULL REFERENCES managers (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (unit_id, period)
);

CREATE INDEX IF NOT EXISTS water_readings_tenant_id_idx ON water_readings (tenant_id);

-- One per property per month, if garbage_enabled.
CREATE TABLE IF NOT EXISTS garbage_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    period date NOT NULL,
    fee_snapshot numeric(12,2) NOT NULL,
    locked boolean NOT NULL DEFAULT false,
    generated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (property_id, period)
);

CREATE INDEX IF NOT EXISTS garbage_runs_tenant_id_idx ON garbage_runs (tenant_id);

-- One per unit per month.
CREATE TABLE IF NOT EXISTS rent_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    period date NOT NULL,
    amount_snapshot numeric(12,2) NOT NULL,
    locked boolean NOT NULL DEFAULT false,
    generated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (unit_id, period)
);

CREATE INDEX IF NOT EXISTS rent_runs_tenant_id_idx ON rent_runs (tenant_id);
