-- The actual split, ties a payment to the charges/ledger entries it settles.
CREATE TABLE IF NOT EXISTS payment_allocations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    payment_id uuid NOT NULL REFERENCES payments ON DELETE CASCADE,
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts ON DELETE CASCADE,
    ledger_entry_id uuid NOT NULL REFERENCES ledger_entries ON DELETE CASCADE,
    amount numeric(12,2) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS payment_allocations_tenant_id_idx ON payment_allocations (tenant_id);
CREATE INDEX IF NOT EXISTS payment_allocations_payment_id_idx ON payment_allocations (payment_id);
