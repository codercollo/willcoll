-- Garbage billing is a per-lease toggle, not a property-wide switch:
-- landlords often don't bill garbage for every unit. properties.garbage_enabled
-- still gates the feature (whether the property bills garbage at all);
-- this decides which occupied units actually get charged. The ledger was
-- always per-unit-per-type already, so water and garbage balances stay
-- separate with no further change.
ALTER TABLE leases ADD COLUMN IF NOT EXISTS garbage_billed boolean NOT NULL DEFAULT false;

-- properties.reconnection_fee: the structured amount behind the water/
-- garbage bill's "NB: reconnection fee of Ksh. X" line. Nullable — no fee
-- configured simply omits the line, never prints a placeholder.
ALTER TABLE properties ADD COLUMN IF NOT EXISTS reconnection_fee numeric(12,2) NULL;
