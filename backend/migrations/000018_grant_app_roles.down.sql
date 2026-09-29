-- Revokes only. The roles are cluster-level and may hold privileges in
-- other databases, so they're left for an operator to drop explicitly.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE SELECT ON TABLES FROM willcoll_admin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM willcoll_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM willcoll_app;

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM willcoll_app, willcoll_admin;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM willcoll_app, willcoll_admin;
REVOKE USAGE ON SCHEMA public FROM willcoll_app, willcoll_admin;
