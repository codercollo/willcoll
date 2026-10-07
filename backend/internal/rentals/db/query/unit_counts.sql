-- CountManagerUnits is the tenant's occupied+vacant unit count across their
-- live properties: what the usage reporter sends platform-api as the
-- billable count (module_usage, build-phases.txt 3e). Runs inside the
-- tenant's own transaction (RLS-scoped).
-- name: CountManagerUnits :one
SELECT count(*)::int FROM units u
JOIN properties p ON p.id = u.property_id
WHERE u.tenant_id = $1 AND p.deleted_at IS NULL;
