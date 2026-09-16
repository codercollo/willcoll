-- Phase 5.1: immutable double-entry ledger + invoices (spec §4).

CREATE TYPE account_owner_kind AS ENUM ('tenant', 'property_till', 'landlord_payable', 'deposit_holding');
CREATE TYPE invoice_kind AS ENUM ('rent', 'water_garbage', 'deposit', 'late_fee');
CREATE TYPE invoice_status AS ENUM ('open', 'partially_paid', 'paid', 'waived');
CREATE TYPE transfer_kind AS ENUM (
    'rent_payment',
    'water_payment',
    'deposit_payment',
    'deposit_refund',
    'remittance_to_landlord',
    'late_fee_charge',
    'reversal'
);
CREATE TYPE payment_method AS ENUM (
    'cash',
    'mpesa_manual',
    'bank',
    'cheque',
    'intasend_mpesa',
    'intasend_card'
);

CREATE TABLE ledger_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    owner_type account_owner_kind NOT NULL,
    owner_id UUID NOT NULL,
    currency TEXT NOT NULL DEFAULT 'KES',
    balance NUMERIC(14,2) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_type, owner_id, currency)
);

CREATE TABLE invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lease_id UUID NOT NULL REFERENCES leases(id) ON DELETE CASCADE,
    period_month DATE NOT NULL,
    invoice_type invoice_kind NOT NULL,
    amount_due NUMERIC(12,2) NOT NULL,
    -- READ-MODEL ONLY, written exclusively by internal/money (see rule 3)
    amount_paid NUMERIC(12,2) NOT NULL DEFAULT 0,
    balance NUMERIC(12,2) GENERATED ALWAYS AS (amount_due - amount_paid) STORED,
    -- READ-MODEL ONLY, written exclusively by internal/money (see rule 3)
    status invoice_status NOT NULL DEFAULT 'open',
    meter_reading_id UUID,  -- FK to meter_readings is added when that table lands
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (lease_id, period_month, invoice_type)
);

CREATE TABLE ledger_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    transfer_type transfer_kind NOT NULL,
    invoice_id UUID REFERENCES invoices(id) ON DELETE SET NULL,
    method payment_method NOT NULL,
    reference TEXT,
    narrative TEXT,
    idempotency_key TEXT UNIQUE NOT NULL,
    reversed_transfer_id UUID REFERENCES ledger_transfers(id) ON DELETE SET NULL,
    recorded_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    amount NUMERIC(14,2) NOT NULL,
    transfer_id UUID NOT NULL REFERENCES ledger_transfers(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RLS on ledger_accounts (direct organization_id).
ALTER TABLE ledger_accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY ledger_accounts_select ON ledger_accounts FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY ledger_accounts_insert ON ledger_accounts FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY ledger_accounts_update ON ledger_accounts FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY ledger_accounts_delete ON ledger_accounts FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

-- RLS on invoices (direct organization_id).
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
CREATE POLICY invoices_select ON invoices FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY invoices_insert ON invoices FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY invoices_update ON invoices FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY invoices_delete ON invoices FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

-- RLS on ledger_transfers (direct organization_id).
ALTER TABLE ledger_transfers ENABLE ROW LEVEL SECURITY;
CREATE POLICY ledger_transfers_select ON ledger_transfers FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY ledger_transfers_insert ON ledger_transfers FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY ledger_transfers_update ON ledger_transfers FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY ledger_transfers_delete ON ledger_transfers FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

-- RLS on ledger_entries (organization_id inherited via account_id).
-- Append-only: only SELECT and INSERT policies exist.
ALTER TABLE ledger_entries ENABLE ROW LEVEL SECURITY;
CREATE POLICY ledger_entries_select ON ledger_entries FOR SELECT USING (
    EXISTS (
        SELECT 1 FROM ledger_accounts a
        WHERE a.id = account_id
          AND a.organization_id = current_setting('app.current_org_id')::uuid
    )
);
CREATE POLICY ledger_entries_insert ON ledger_entries FOR INSERT WITH CHECK (
    EXISTS (
        SELECT 1 FROM ledger_accounts a
        WHERE a.id = account_id
          AND a.organization_id = current_setting('app.current_org_id')::uuid
    )
);



