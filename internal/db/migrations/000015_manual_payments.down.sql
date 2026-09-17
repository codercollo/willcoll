DROP INDEX IF EXISTS ledger_transfers_org_reference_number_unique;

ALTER TABLE ledger_transfers
    DROP COLUMN IF EXISTS manual_payment_method,
    DROP COLUMN IF EXISTS reference_number,
    DROP COLUMN IF EXISTS receipt_photo_url;

DROP TYPE IF EXISTS manual_payment_method;
