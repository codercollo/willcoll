-- Created the instant a tenant submits the PayIntentForm.
CREATE TABLE IF NOT EXISTS payment_intents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES units ON DELETE CASCADE,
    lines jsonb NOT NULL,
    external_reference text UNIQUE NOT NULL,
    phone text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS payment_intents_tenant_id_idx ON payment_intents (tenant_id);

-- Raw inbound money, one row per webhook. mpesa_receipt is the primary
-- idempotency key (system-design.txt 3.8).
CREATE TABLE IF NOT EXISTS payments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    source text NOT NULL CHECK (source IN ('payhero_stk', 'payhero_c2b', 'manual')),
    mpesa_receipt text UNIQUE NOT NULL,
    payhero_reference text NULL,
    amount numeric(12,2) NOT NULL,
    msisdn text NOT NULL,
    payer_name text NULL,
    account_reference text NULL,
    matched_unit_id uuid NULL REFERENCES units (id),
    matched_intent_id uuid NULL REFERENCES payment_intents (id),
    status text NOT NULL DEFAULT 'unmatched' CHECK (status IN ('unmatched', 'matched', 'allocated')),
    raw_payload jsonb NOT NULL,
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS payments_tenant_id_idx ON payments (tenant_id);
CREATE INDEX IF NOT EXISTS payments_matched_unit_id_idx ON payments (matched_unit_id);
