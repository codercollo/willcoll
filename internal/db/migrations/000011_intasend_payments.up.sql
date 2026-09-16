-- IntaSend payment gateway (spec §3.1b, §5.5).
-- manager_payout_accounts is the Manager settlement identity (KYC'd by
-- IntaSend); payment_gateway_transactions is the webhook inbox whose
-- UNIQUE (gateway_provider, gateway_reference) constraint is the
-- duplicate-payment guard.

CREATE TYPE payout_doc_kind AS ENUM ('certificate_of_incorporation', 'national_id');
CREATE TYPE payout_kyc_status AS ENUM ('pending', 'verified', 'rejected');

CREATE TABLE manager_payout_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    manager_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    bank_name TEXT NOT NULL,
    bank_branch_code TEXT,
    account_name TEXT NOT NULL,
    account_number TEXT NOT NULL,
    kra_pin TEXT NOT NULL,
    business_doc_type payout_doc_kind NOT NULL,
    business_doc_reference TEXT NOT NULL, -- pointer to the stored KYC document, not the raw file
    gateway_provider TEXT NOT NULL DEFAULT 'intasend',
    gateway_subaccount_id TEXT UNIQUE,    -- set once IntaSend clears KYC
    kyc_status payout_kyc_status NOT NULL DEFAULT 'pending',
    kyc_submitted_at TIMESTAMPTZ,
    kyc_verified_at TIMESTAMPTZ,
    kyc_rejection_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE payment_gateway_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, -- resolved at initiation time
    gateway_provider TEXT NOT NULL DEFAULT 'intasend',
    gateway_reference TEXT NOT NULL,      -- IntaSend's checkout/invoice/transaction ID
    gateway_subaccount_id TEXT NOT NULL,  -- which Manager this settled to
    unit_id UUID REFERENCES units(id) ON DELETE SET NULL,
    invoice_id UUID REFERENCES invoices(id) ON DELETE SET NULL,
    amount NUMERIC(12,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'KES',
    channel TEXT NOT NULL,                -- 'mpesa' | 'card'
    status TEXT NOT NULL,                 -- 'PENDING' | 'COMPLETE' | 'FAILED'
    raw_payload JSONB NOT NULL,           -- full webhook body, kept for dispute/audit
    ledger_transfer_id UUID REFERENCES ledger_transfers(id) ON DELETE SET NULL, -- set once posted
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (gateway_provider, gateway_reference)
);

ALTER TABLE manager_payout_accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY manager_payout_accounts_select ON manager_payout_accounts FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY manager_payout_accounts_insert ON manager_payout_accounts FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY manager_payout_accounts_update ON manager_payout_accounts FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY manager_payout_accounts_delete ON manager_payout_accounts FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

ALTER TABLE payment_gateway_transactions ENABLE ROW LEVEL SECURITY;
CREATE POLICY payment_gateway_transactions_select ON payment_gateway_transactions FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY payment_gateway_transactions_insert ON payment_gateway_transactions FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY payment_gateway_transactions_update ON payment_gateway_transactions FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY payment_gateway_transactions_delete ON payment_gateway_transactions FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);
