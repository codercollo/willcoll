-- Re-confirming a stale period must not lose the prior confirmation: keep
-- every superseded snapshot in history instead of letting the UPSERT in
-- report_confirmations silently discard it (the 000031 comment calling
-- re-confirm "a plain UPSERT" only covered the live gate row, not an
-- audit trail of past confirmations).
CREATE TABLE IF NOT EXISTS report_confirmation_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    period date NOT NULL,
    totals_checksum text NOT NULL,
    note_lines jsonb NOT NULL,
    confirmed_by uuid NOT NULL REFERENCES managers (id),
    confirmed_at timestamptz NOT NULL,
    superseded_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS report_confirmation_history_property_period_idx
    ON report_confirmation_history (property_id, period);
CREATE INDEX IF NOT EXISTS report_confirmation_history_tenant_id_idx
    ON report_confirmation_history (tenant_id);

ALTER TABLE report_confirmation_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_confirmation_history FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report_confirmation_history
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- Append-only audit trail: never updated or deleted, same as the ledger
-- (ADR 0002), even though this table sits outside the ledger core.
REVOKE UPDATE, DELETE ON report_confirmation_history FROM willcoll_app;
