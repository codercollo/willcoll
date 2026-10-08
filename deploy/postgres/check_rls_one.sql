-- Included by check_rls.sql once per database, with psql variables app_role and
-- admin_role (empty when the database has no admin role). The function is
-- temporary: it lives only in the connection that runs it.

CREATE OR REPLACE FUNCTION pg_temp.check_one(app_role text, admin_role text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    r record;
    protected int := 0;
    visible_to_super bigint := 0;
    n bigint;
    leaked bigint := 0;
BEGIN
    -- 1. attributes and ownership
    SELECT rolsuper, rolbypassrls INTO r FROM pg_roles WHERE rolname = app_role;
    IF NOT FOUND THEN RAISE EXCEPTION '% does not exist', app_role; END IF;
    IF r.rolsuper THEN RAISE EXCEPTION '% is a superuser: RLS would not apply', app_role; END IF;
    IF r.rolbypassrls THEN RAISE EXCEPTION '% has BYPASSRLS: RLS would not apply', app_role; END IF;

    IF EXISTS (SELECT 1 FROM pg_database d JOIN pg_roles o ON o.oid = d.datdba
               WHERE d.datname = current_database() AND o.rolname = app_role) THEN
        RAISE EXCEPTION '% owns database %: RLS would not apply to it', app_role, current_database();
    END IF;
    SELECT count(*) INTO n FROM pg_class c JOIN pg_roles o ON o.oid = c.relowner
        WHERE o.rolname = app_role AND c.relnamespace = 'public'::regnamespace;
    IF n > 0 THEN RAISE EXCEPTION '% owns % objects in public: RLS would not apply to them', app_role, n; END IF;

    IF admin_role IS NOT NULL THEN
        SELECT rolsuper, rolbypassrls INTO r FROM pg_roles WHERE rolname = admin_role;
        IF r.rolsuper OR NOT r.rolbypassrls THEN
            RAISE EXCEPTION '% must be BYPASSRLS and not a superuser', admin_role;
        END IF;
    END IF;

    -- 2. no schema changes
    EXECUTE format('SET ROLE %I', app_role);
    BEGIN
        EXECUTE 'CREATE TABLE public.willcoll_rls_probe (x int)';
        RAISE EXCEPTION '% could create a table', app_role;
    EXCEPTION WHEN insufficient_privilege THEN
        NULL; -- expected
    END;
    RESET ROLE;

    -- 3. behaviour: as the app role, a tenant with no data sees nothing
    FOR r IN SELECT c.oid::regclass AS tbl FROM pg_class c
             WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r' AND c.relrowsecurity
             ORDER BY c.relname LOOP
        protected := protected + 1;
        EXECUTE format('SELECT count(*) FROM %s', r.tbl) INTO n;   -- as the superuser
        visible_to_super := visible_to_super + n;

        EXECUTE format('SET ROLE %I', app_role);
        PERFORM set_config('app.tenant_id', gen_random_uuid()::text, false);
        BEGIN
            EXECUTE format('SELECT count(*) FROM %s', r.tbl) INTO n;
            leaked := leaked + n;
            IF n > 0 THEN RAISE WARNING '%: % rows visible to a tenant that owns none', r.tbl, n; END IF;
        EXCEPTION WHEN insufficient_privilege THEN
            NULL; -- no SELECT grant at all is also isolation
        END;
        RESET ROLE;
    END LOOP;
    PERFORM set_config('app.tenant_id', '', false);

    IF protected = 0 THEN RAISE EXCEPTION '% has no RLS-protected tables: nothing to prove', current_database(); END IF;
    IF leaked > 0 THEN RAISE EXCEPTION '% rows leaked across tenants through %', leaked, app_role; END IF;

    RAISE NOTICE '%: % is not superuser/BYPASSRLS/owner, cannot alter the schema; % RLS tables show 0 rows to a foreign tenant (superuser sees % rows)%',
        current_database(), app_role, protected, visible_to_super,
        CASE WHEN visible_to_super = 0 THEN ' [database is empty: run staging/seed and re-check for the behavioural proof]' ELSE '' END;
END
$$;


SELECT pg_temp.check_one(:'app_role', NULLIF(:'admin_role', ''));
