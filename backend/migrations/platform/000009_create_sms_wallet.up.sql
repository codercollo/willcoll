-- Prepaid SMS units per manager (system-design.txt A.7): OTPs and payment
-- confirmations are free; reminders, bulk sends and notices debit this
-- balance. balance_units is a cached total, always re-derivable by summing
-- sms_ledger — same "balance is never trusted alone" pattern as a product's
-- ledger, kept here as a fast-path read rather than re-summing on every
-- send-eligibility check.
CREATE TABLE IF NOT EXISTS sms_wallets (
    manager_id uuid PRIMARY KEY REFERENCES managers ON DELETE CASCADE,
    balance_units int NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only: every top-up and debit is a row, never an UPDATE to a past
-- entry. delta_units is positive for a top-up, negative for a debit.
CREATE TABLE IF NOT EXISTS sms_ledger (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    delta_units int NOT NULL CHECK (delta_units <> 0),
    reason text NOT NULL CHECK (reason IN ('topup', 'reminder', 'bulk_send', 'notice', 'adjustment')),
    reference text NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sms_ledger_manager_id_created_at_idx ON sms_ledger (manager_id, created_at DESC);
