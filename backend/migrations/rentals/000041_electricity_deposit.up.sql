-- Electricity deposit: a one-off refundable deposit like the water deposit,
-- not a monthly bill. Same shape as garbage_enabled/garbage_fee (property
-- toggle + default amount) and rent_deposit_amount/water_deposit_amount
-- (per-lease amount, prefilled from the property default but editable).
ALTER TABLE properties ADD COLUMN IF NOT EXISTS electricity_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE properties ADD COLUMN IF NOT EXISTS electricity_deposit_amount numeric(12,2) NOT NULL DEFAULT 0;

ALTER TABLE leases ADD COLUMN IF NOT EXISTS electricity_deposit_amount numeric(12,2) NOT NULL DEFAULT 0;

-- New ledger type, append-only like every other (ADR 0002): charged once at
-- lease start (or when electricity is switched on for an existing active
-- lease, guarded against a duplicate charge), paid down through the same
-- allocation engine as RENT_DEPOSIT/WATER_DEPOSIT, refunded or offset at
-- termination the same way (manually, via the existing ledger tools — there
-- is no automated deposit refund for water either).
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_type_check;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_type_check
    CHECK (type IN ('RENT', 'WATER', 'GARBAGE', 'RENT_DEPOSIT', 'WATER_DEPOSIT', 'ELECTRICITY_DEPOSIT'));

ALTER TABLE receipts DROP CONSTRAINT IF EXISTS receipts_ledger_type_check;
ALTER TABLE receipts ADD CONSTRAINT receipts_ledger_type_check
    CHECK (ledger_type IN ('RENT', 'WATER', 'GARBAGE', 'RENT_DEPOSIT', 'WATER_DEPOSIT', 'ELECTRICITY_DEPOSIT'));
