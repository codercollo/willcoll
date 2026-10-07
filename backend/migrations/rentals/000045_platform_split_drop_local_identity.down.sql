-- Deliberately unsupported (ADR-0006's Decision 3 and Consequences):
-- recreating admins/managers/tokens/subscription_plans/subscriptions/
-- subscription_invoices from scratch would not restore the data this
-- migration dropped, and re-adding the tenant_id FKs blind risks adding
-- them against rows an application bug already let through unvalidated in
-- the meantime. The only real rollback is restoring the pre-cutover
-- pg_dump taken by scripts/backup_before_split.sh before this migration
-- ever ran. Failing loudly here is more honest than a down migration that
-- LOOKS like it undoes this but doesn't.
DO $$
BEGIN
    RAISE EXCEPTION 'migration 000045 has no down: restore the pre-cutover pg_dump from scripts/backup_before_split.sh instead (see ADR-0006)';
END $$;
