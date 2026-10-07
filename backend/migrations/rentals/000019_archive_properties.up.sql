-- Properties are archived, never deleted. A hard DELETE would cascade
-- through units into charges and ledger_entries (the cascade runs with the
-- table owner's rights, so the append-only grants wouldn't stop it) and
-- wipe ledger history. DELETE /v1/properties/:id sets deleted_at instead,
-- and every property query filters on deleted_at IS NULL.
ALTER TABLE properties ADD COLUMN deleted_at timestamptz NULL;

-- Hard deletes are off the table for the API role. charges and
-- ledger_entries were already revoked in 000018 (append-only, ADR 0002);
-- restated here so this migration is the single place that lists every
-- table the API can never DELETE from.
REVOKE DELETE ON properties, charges, ledger_entries FROM willcoll_app;
