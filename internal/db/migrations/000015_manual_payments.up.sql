-- Manual Payment Recording, Phase 1.1 (spec §4.3 policy layer).
--
-- Payment method here is metadata only — it never drives what the payment is
-- FOR. Purpose (rent/arrears/water/garbage/deposit) already comes from
-- invoices.invoice_type via ledger_transfers.invoice_id, unchanged by this
-- migration; a Manager picks which invoice(s) to apply an amount to, never
-- types a free-text purpose.
--
-- This adds a manual-entry-specific method taxonomy and evidence trail
-- alongside the existing ledger_transfers.method/reference/recorded_by
-- columns (kept as-is — payment_method's intasend_mpesa/intasend_card values
-- and every other transfer_kind still use them exactly as before). The new
-- columns are nullable and only populated when a Manager records a manual
-- payment; every other transfer keeps them NULL.

CREATE TYPE manual_payment_method AS ENUM ('cash', 'mpesa', 'bank', 'card');

ALTER TABLE ledger_transfers
    ADD COLUMN manual_payment_method manual_payment_method,
    ADD COLUMN reference_number TEXT,
    ADD COLUMN receipt_photo_url TEXT;

-- recorded_by_user_id is deliberately not added: ledger_transfers.recorded_by
-- (FK users NOT NULL) already carries this for every transfer, manual or not.

-- Scoped to manual entries only (reference_number IS NULL for every other
-- transfer_kind), so this never collides with remittances/refunds/reversals
-- that don't carry one. Blocks the same M-Pesa code/receipt number from
-- being recorded twice within one Organization.
CREATE UNIQUE INDEX ledger_transfers_org_reference_number_unique
    ON ledger_transfers (organization_id, reference_number)
    WHERE reference_number IS NOT NULL;
