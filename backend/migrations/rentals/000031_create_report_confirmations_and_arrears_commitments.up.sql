-- Period confirmation gate (system-design.txt PDF/reconciliation additions):
-- a manager reviews the sanity-checked report for a property/period and
-- confirms it. The checksum is a hash of the confirmed totals; every PDF
-- request recomputes it against the live ledger and 409s if it no longer
-- matches, instead of relying on a trigger to flag staleness.
--
-- note_lines freezes the rendered NOTE:1/NOTE:2 text (derived by FIFO over
-- the ledger at confirmation time) so a confirmed PDF always renders from
-- this snapshot, never a live re-derivation that could drift after later
-- payments change the FIFO order.
--
-- This table is not part of the append-only ledger core (ADR 0002) — it is
-- a service-level gate, not a financial record — so re-confirming after a
-- stale result is a plain UPSERT, not a storno reversal.
CREATE TABLE IF NOT EXISTS report_confirmations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    property_id uuid NOT NULL REFERENCES properties ON DELETE CASCADE,
    period date NOT NULL,
    totals_checksum text NOT NULL,
    note_lines jsonb NOT NULL,
    confirmed_by uuid NOT NULL REFERENCES managers (id),
    confirmed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (property_id, period)
);

CREATE INDEX IF NOT EXISTS report_confirmations_tenant_id_idx ON report_confirmations (tenant_id);

-- Commitment letters: the one piece of NOTE:2 that is a real paper
-- document, not something derivable from the ledger. Everything else in
-- NOTE:2 (advances, arrears cleared/carried) is computed straight from
-- payment_allocations / charges / unit_ledger_balances.
CREATE TABLE IF NOT EXISTS arrears_commitments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    period date NOT NULL,
    note text NOT NULL,
    created_by uuid NOT NULL REFERENCES managers (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS arrears_commitments_tenant_id_idx ON arrears_commitments (tenant_id);
CREATE INDEX IF NOT EXISTS arrears_commitments_unit_id_idx ON arrears_commitments (unit_id);

ALTER TABLE report_confirmations ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_confirmations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report_confirmations
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE arrears_commitments ENABLE ROW LEVEL SECURITY;
ALTER TABLE arrears_commitments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON arrears_commitments
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
