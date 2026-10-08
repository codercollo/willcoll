CREATE OR REPLACE FUNCTION app_property_scope_active() RETURNS boolean
LANGUAGE sql STABLE AS
$$ SELECT coalesce(current_setting('app.property_ids', true), '') <> '' $$;

CREATE OR REPLACE FUNCTION app_property_in_scope(pid uuid) RETURNS boolean
LANGUAGE sql STABLE AS
$$ SELECT NOT app_property_scope_active()
       OR (pid IS NOT NULL
           AND pid = ANY (string_to_array(current_setting('app.property_ids', true), ',')::uuid[])) $$;
