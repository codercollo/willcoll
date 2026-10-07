-- A rent receipt's period is when the payment was received (grouping and
-- numbering scope, ListReceiptsForPeriod), which is not always the same
-- month as the charge it actually paid off: a payment can clear an older
-- unpaid month. settled_period is that older month, filled in only when
-- the payment genuinely cleared carried-over arrears (not just this
-- period's own just-posted charge) — the receipt's rent line then names
-- the month actually settled instead of the period it was received in.
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS settled_period date NULL;
