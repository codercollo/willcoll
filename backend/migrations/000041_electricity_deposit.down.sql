ALTER TABLE receipts DROP CONSTRAINT IF EXISTS receipts_ledger_type_check;
ALTER TABLE receipts ADD CONSTRAINT receipts_ledger_type_check
    CHECK (ledger_type IN ('RENT', 'WATER', 'GARBAGE', 'RENT_DEPOSIT', 'WATER_DEPOSIT'));

ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_type_check;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_type_check
    CHECK (type IN ('RENT', 'WATER', 'GARBAGE', 'RENT_DEPOSIT', 'WATER_DEPOSIT'));

ALTER TABLE leases DROP COLUMN IF EXISTS electricity_deposit_amount;

ALTER TABLE properties DROP COLUMN IF EXISTS electricity_deposit_amount;
ALTER TABLE properties DROP COLUMN IF EXISTS electricity_enabled;
