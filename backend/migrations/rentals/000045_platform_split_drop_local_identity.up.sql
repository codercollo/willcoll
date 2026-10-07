-- Platform split, final step (ADR-0006, build-phases.txt 3i). This
-- database stops owning identity and billing — willcoll_platform (migrated
-- and backfilled well before this runs) does. There is NO automatic
-- rollback past this point: ADR-0006's recovery path is restoring the
-- pre-cutover pg_dump (scripts/backup_before_split.sh), never a down
-- migration (see this file's .down.sql).
--
-- Every tenant-scoped table's tenant_id (and every created_by/recorded_by/
-- confirmed_by/generated_by audit column) is a REFERENCES managers foreign
-- key today. Once `managers` is dropped below, Postgres cannot keep that
-- constraint — there is no cross-database FK. Found generically via
-- pg_constraint rather than hand-listing ~20 tables' auto-generated
-- constraint names: hand-listing them is exactly the kind of thing that
-- silently goes stale the next time a table's FK is recreated with a
-- different name, and a generic drop can't miss one.
DO $$
DECLARE
    rec record;
BEGIN
    FOR rec IN
        SELECT conrelid::regclass AS table_name, conname
        FROM pg_constraint
        WHERE confrelid = 'managers'::regclass AND contype = 'f'
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', rec.table_name, rec.conname);
    END LOOP;
END $$;

-- The platform-owned tables this database carried before the split
-- (ADR-0006's context list), in FK-safe order. Validated against
-- platform.managers at the application layer now (session/auth middleware
-- resolves and trusts the manager id before any tenant-scoped query runs)
-- — a deliberate, documented loosening, not an oversight.
DROP TABLE IF EXISTS subscription_invoices;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS subscription_plans;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS admins;
DROP TABLE IF EXISTS managers;
