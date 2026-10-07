-- AdminListTenantIDs is every tenant that has ever had a property, for the
-- daily usage recount sweep (build-phases.txt 3e). Rentals no longer has a
-- managers table (3i-a — identity lives in willcoll_platform), so "every
-- tenant" is read from the tenant data itself. Includes archived
-- properties' tenants on purpose: their count must drop to zero too.
-- Runs ONLY on the willcoll_admin pool (BYPASSRLS): under willcoll_app,
-- RLS would hide every tenant's rows.
-- name: AdminListTenantIDs :many
SELECT DISTINCT tenant_id FROM properties
ORDER BY tenant_id;
