-- WHAT IS OWED (append-only). Charges are never UPDATEd or DELETEd; a wrong
-- charge is corrected by inserting a negative-amount charge with
-- source_type='manual' referencing the original charge id (storno pattern).
CREATE TABLE IF NOT EXISTS charges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts ON DELETE CASCADE,
    source_type text NOT NULL CHECK (source_type IN ('rent_run', 'water_reading', 'garbage_run', 'lease_start', 'manual')),
    source_id uuid NULL,
    period date NOT NULL,
    amount numeric(12,2) NOT NULL,
    description text NOT NULL DEFAULT '',
    created_by uuid NOT NULL REFERENCES managers (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS charges_tenant_id_idx ON charges (tenant_id);
CREATE INDEX IF NOT EXISTS charges_ledger_account_id_idx ON charges (ledger_account_id);
