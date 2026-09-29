-- Only properties' DELETE came from 000018's baseline grant; charges and
-- ledger_entries stay revoked, as 000018 left them.
GRANT DELETE ON properties TO willcoll_app;

ALTER TABLE properties DROP COLUMN IF EXISTS deleted_at;
