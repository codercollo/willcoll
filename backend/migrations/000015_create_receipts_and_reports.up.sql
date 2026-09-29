-- Mirrors the KIWI PLACE carbon receipt book.
CREATE TABLE IF NOT EXISTS receipts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    receipt_no bigint NOT NULL,
    payment_ids uuid[] NOT NULL,
    period date NOT NULL,
    pdf_object_key text NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, receipt_no)
);

CREATE INDEX IF NOT EXISTS receipts_tenant_id_idx ON receipts (tenant_id);
CREATE INDEX IF NOT EXISTS receipts_unit_id_idx ON receipts (unit_id);

-- The "ALL IN ONE PAYMENTS SCHEDULE".
CREATE TABLE IF NOT EXISTS reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    period date NOT NULL,
    totals jsonb NOT NULL,
    pdf_object_key text NOT NULL,
    generated_by uuid NOT NULL REFERENCES managers (id),
    generated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (property_id, period)
);

CREATE INDEX IF NOT EXISTS reports_tenant_id_idx ON reports (tenant_id);
