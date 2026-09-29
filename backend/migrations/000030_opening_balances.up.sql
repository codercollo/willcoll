-- Opening balances: bringing a client's existing position (arrears owed, deposits
-- already held) into an append-only ledger at go-live, without pretending anyone
-- paid anything today.
--
--   transaction_headers.type   + 'OPENING_BALANCE'
--   charges.source_type        + 'opening_balance'   (an arrears amount owed at go-live)
--   ledger_entries.reference_type + 'opening_balance' (a CREDIT that settles a deposit
--                                  the tenant paid before Willcoll: it is not a payment,
--                                  so it never appears as money received in a report)
--
-- The CHECK constraints were created unnamed, so find them by what they check.
DO $$
DECLARE r record;
BEGIN
    FOR r IN
        SELECT c.conrelid::regclass AS tbl, c.conname
        FROM pg_constraint c
        WHERE c.contype = 'c'
          AND ((c.conrelid = 'transaction_headers'::regclass AND pg_get_constraintdef(c.oid) LIKE '%RENT_RUN%')
            OR (c.conrelid = 'charges'::regclass AND pg_get_constraintdef(c.oid) LIKE '%water_reading%')
            OR (c.conrelid = 'ledger_entries'::regclass AND pg_get_constraintdef(c.oid) LIKE '%payment_allocation%'))
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', r.tbl, r.conname);
    END LOOP;
END $$;

ALTER TABLE transaction_headers ADD CONSTRAINT transaction_headers_type_check
    CHECK (type IN ('RENT_RUN', 'WATER_RUN', 'GARBAGE_RUN', 'LEASE_START', 'PAYMENT_POSTING', 'MANUAL_ADJUSTMENT', 'REVERSAL', 'OPENING_BALANCE'));
ALTER TABLE charges ADD CONSTRAINT charges_source_type_check
    CHECK (source_type IN ('rent_run', 'water_reading', 'garbage_run', 'lease_start', 'manual', 'opening_balance'));
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_reference_type_check
    CHECK (reference_type IN ('charge', 'payment_allocation', 'reversal', 'opening_balance'));
