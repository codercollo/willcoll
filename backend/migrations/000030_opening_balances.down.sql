-- Refuses to run if opening-balance rows exist (they would violate the old checks).
ALTER TABLE transaction_headers DROP CONSTRAINT IF EXISTS transaction_headers_type_check;
ALTER TABLE transaction_headers ADD CONSTRAINT transaction_headers_type_check
    CHECK (type IN ('RENT_RUN', 'WATER_RUN', 'GARBAGE_RUN', 'LEASE_START', 'PAYMENT_POSTING', 'MANUAL_ADJUSTMENT', 'REVERSAL'));
ALTER TABLE charges DROP CONSTRAINT IF EXISTS charges_source_type_check;
ALTER TABLE charges ADD CONSTRAINT charges_source_type_check
    CHECK (source_type IN ('rent_run', 'water_reading', 'garbage_run', 'lease_start', 'manual'));
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_reference_type_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_reference_type_check
    CHECK (reference_type IN ('charge', 'payment_allocation', 'reversal'));
