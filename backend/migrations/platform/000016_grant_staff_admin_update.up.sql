-- willcoll_platform_admin needs UPDATE on staff (build-phases.txt 3f-1):
-- activation and login resolution run cross-tenant, before a manager
-- session exists, the same reason it already has UPDATE on managers/
-- subscriptions/manager_modules (migration 000013) — that grant predates
-- staff having a cross-tenant write path of its own.
GRANT UPDATE ON staff TO willcoll_platform_admin;
