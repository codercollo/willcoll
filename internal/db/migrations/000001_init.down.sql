-- Reverse Phase 1. The circular organizations<->users FK is dropped first, then
-- tables are dropped in dependency order, then the enum types.
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_owner_user_id_fkey;

DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

DROP TYPE IF EXISTS user_status;
DROP TYPE IF EXISTS user_role;
DROP TYPE IF EXISTS org_billing_status;
DROP TYPE IF EXISTS subscription_tier;
