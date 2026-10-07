-- Groups a set of ledger entries atomically.
CREATE TABLE IF NOT EXISTS transaction_headers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    type text NOT NULL CHECK (type IN ('RENT_RUN', 'WATER_RUN', 'GARBAGE_RUN', 'LEASE_START', 'PAYMENT_POSTING', 'MANUAL_ADJUSTMENT', 'REVERSAL')),
    idempotency_key text UNIQUE NOT NULL,
    description text NOT NULL DEFAULT '',
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transaction_headers_tenant_id_idx ON transaction_headers (tenant_id);

-- THE immutable ledger. Never UPDATE/DELETE. Balance is never a stored
-- column; see the unit_ledger_balances view in the next migration.
CREATE TABLE IF NOT EXISTS ledger_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    transaction_header_id uuid NOT NULL REFERENCES transaction_headers ON DELETE CASCADE,
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts ON DELETE CASCADE,
    direction text NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount numeric(12,2) NOT NULL CHECK (amount > 0),
    reference_type text NOT NULL CHECK (reference_type IN ('charge', 'payment_allocation', 'reversal')),
    reference_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ledger_entries_tenant_id_idx ON ledger_entries (tenant_id);
CREATE INDEX IF NOT EXISTS ledger_entries_account_created_idx ON ledger_entries (ledger_account_id, created_at);
