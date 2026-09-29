-- Units (system-design.txt 3.2). Tenant-scoped under RLS.

-- PropertyIsActive guards every write that takes a property_id: the FK
-- check ignores RLS, and archived properties (000019) take no new units.
-- name: PropertyIsActive :one
SELECT EXISTS (
    SELECT 1 FROM properties
    WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
);

-- name: CreateUnit :one
INSERT INTO units (tenant_id, property_id, unit_code, meter_number, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- SetUnitStatus is the only way a unit's status changes: occupied when a
-- lease starts (or is reactivated), vacant when it's terminated, in the
-- same transaction as that lease write (data.LeaseModel). It bumps version
-- so a unit edit based on the old row gets an edit conflict.
-- name: SetUnitStatus :exec
UPDATE units
SET status = @status, version = version + 1
WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetUnit :one
SELECT * FROM units
WHERE tenant_id = $1 AND id = $2;

-- ListUnitsForProperty pages a property's units for the Units tab: each
-- with its current (active) lease's tenant and start date, for the unit
-- cards (system-design.txt 6.1). Optional filters:
--   status        vacant | occupied
--   tenant_query  a prefix tsquery built by the service layer (e.g.
--                 'wanj:* & jane:*'), matched against the active lease's
--                 tenant_name or any of its lease_payers' names. This is
--                 the API's one full-text search that earns its keep
--                 (Greenlight ch.9.5); the GIN indexes are in 000021.
-- name: ListUnitsForProperty :many
SELECT count(*) OVER() AS total_records,
       sqlc.embed(units),
       active_lease.id AS lease_id,
       active_lease.tenant_name AS lease_tenant_name,
       active_lease.start_date AS lease_start_date,
       -- whether the lease has a live door-sticker code (migration 000028)
       COALESCE(EXISTS (
           SELECT 1 FROM unit_qr_codes q
           WHERE q.tenant_id = units.tenant_id AND q.lease_id = active_lease.id AND q.revoked_at IS NULL
       ), false)::bool AS has_qr
FROM units
LEFT JOIN leases active_lease
       ON active_lease.unit_id = units.id AND active_lease.status = 'active'
WHERE units.tenant_id = @tenant_id
  AND units.property_id = @property_id
  AND (sqlc.narg('status')::text IS NULL OR units.status = sqlc.narg('status')::text)
  AND (@tenant_query::text = '' OR EXISTS (
        SELECT 1
        FROM leases l
        LEFT JOIN lease_payers lp ON lp.lease_id = l.id
        WHERE l.unit_id = units.id
          AND l.status = 'active'
          AND (to_tsvector('simple', l.tenant_name) @@ to_tsquery('simple', @tenant_query::text)
               OR to_tsvector('simple', lp.name) @@ to_tsquery('simple', @tenant_query::text))
      ))
ORDER BY
    CASE WHEN @sort::text = 'unit_code' THEN units.unit_code END ASC,
    CASE WHEN @sort::text = '-unit_code' THEN units.unit_code END DESC,
    CASE WHEN @sort::text = 'created_at' THEN units.created_at END ASC,
    CASE WHEN @sort::text = '-created_at' THEN units.created_at END DESC,
    units.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- UpdateUnit: optimistic concurrency; zero rows means the version moved on.
-- status is deliberately not settable here: it follows the unit's lease
-- (SetUnitStatus), so a manual edit can never contradict the lease record.
-- name: UpdateUnit :one
UPDATE units
SET unit_code = @unit_code,
    meter_number = @meter_number,
    version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND version = @version
RETURNING version;

-- ListUnitCodes is every unit code on a property, for the bulk import to
-- report clashes before it writes anything.
-- name: ListUnitCodes :many
SELECT unit_code FROM units
WHERE tenant_id = $1 AND property_id = $2;
