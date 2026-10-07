REVOKE ALL ON ALL TABLES IN SCHEMA public FROM willcoll_platform_app, willcoll_platform_admin;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM willcoll_platform_app, willcoll_platform_admin;
REVOKE USAGE ON SCHEMA public FROM willcoll_platform_app, willcoll_platform_admin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM willcoll_platform_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE USAGE, SELECT ON SEQUENCES FROM willcoll_platform_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT ON TABLES FROM willcoll_platform_admin;
