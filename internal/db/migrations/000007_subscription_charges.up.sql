-- Phase 8.1: Willcoll's own subscription billing. This table is deliberately
-- unrelated to any tenant rent ledger / invoice / ledger_account.

CREATE TABLE subscription_charges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    manager_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    tier subscription_tier NOT NULL,
    amount_kes NUMERIC(14,2) NOT NULL,
    gateway_provider TEXT NOT NULL DEFAULT 'intasend',
    gateway_reference TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    billing_period_start DATE NOT NULL,
    billing_period_end DATE NOT NULL,
    raw_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT subscription_charges_status_check CHECK (status IN ('PENDING', 'COMPLETE', 'FAILED')),
    CONSTRAINT subscription_charges_gateway_unique UNIQUE (gateway_provider, gateway_reference)
);

-- Manager-scoped data, isolated by the Organization boundary like every other
-- business-data table.
ALTER TABLE subscription_charges ENABLE ROW LEVEL SECURITY;
CREATE POLICY subscription_charges_select ON subscription_charges FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY subscription_charges_insert ON subscription_charges FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY subscription_charges_update ON subscription_charges FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY subscription_charges_delete ON subscription_charges FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);
