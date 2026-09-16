-- electricity_topups (spec §5.4) — tenant self-service electricity topup log.
-- Informational only: no ledger_entries reference this table, so it never
-- affects the ledger. organization_id is inherited via unit_id, so the RLS
-- policy resolves it transitively (same pattern as meters, 000006).

CREATE TABLE electricity_topups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    unit_id UUID NOT NULL REFERENCES units(id) ON DELETE CASCADE,
    reported_by UUID REFERENCES tenants(id) ON DELETE SET NULL, -- self-reported via inbound SMS
    amount NUMERIC(10,2),
    token_reference TEXT,
    reported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only informational log: only SELECT and INSERT policies exist.
ALTER TABLE electricity_topups ENABLE ROW LEVEL SECURITY;
CREATE POLICY electricity_topups_select ON electricity_topups FOR SELECT USING (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY electricity_topups_insert ON electricity_topups FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1
        FROM units u
        JOIN properties p ON p.id = u.property_id
        WHERE u.id = unit_id
          AND p.organization_id = current_setting('app.current_org_id')::uuid
    )
);
