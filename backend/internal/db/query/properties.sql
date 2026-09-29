-- Properties (system-design.txt 3.2, 4.6). Every query also filters on
-- tenant_id: RLS is the enforcing layer, this is the belt to its braces.
-- Archived properties (deleted_at set, migration 000019) are invisible to
-- every read and update below.

-- name: CreateProperty :one
INSERT INTO properties (
    tenant_id, landlord_id, name, location, slug, garbage_enabled,
    garbage_fee, electricity_enabled, electricity_deposit_amount,
    water_rate_per_unit, management_fee_percent, payhero_channel_id,
    underground_capacity_units, rooftop_capacity_units, reconnection_fee
) VALUES (
    @tenant_id, @landlord_id, @name, @location, @slug, @garbage_enabled,
    @garbage_fee, @electricity_enabled, @electricity_deposit_amount,
    @water_rate_per_unit, @management_fee_percent, @payhero_channel_id,
    @underground_capacity_units, @rooftop_capacity_units, @reconnection_fee
)
RETURNING *;

-- name: GetProperty :one
SELECT * FROM properties
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL;

-- ListProperties pages a tenant's properties, optionally filtered to one
-- landlord. The sort key comes from the handler's safelist; each CASE arm
-- is NULL unless selected, so only one ordering applies, with id as the
-- stable tie-breaker.
-- name: ListProperties :many
SELECT count(*) OVER() AS total_records, sqlc.embed(properties)
FROM properties
WHERE tenant_id = @tenant_id
  AND deleted_at IS NULL
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR landlord_id = sqlc.narg('landlord_id')::uuid)
ORDER BY
    CASE WHEN @sort::text = 'name' THEN name END ASC,
    CASE WHEN @sort::text = '-name' THEN name END DESC,
    CASE WHEN @sort::text = 'created_at' THEN created_at END ASC,
    CASE WHEN @sort::text = '-created_at' THEN created_at END DESC,
    id ASC
LIMIT @page_limit OFFSET @page_offset;

-- UpdateProperty applies optimistic concurrency control: zero rows (and so
-- sql.ErrNoRows) means the version moved on since the caller read it.
-- name: UpdateProperty :one
UPDATE properties
SET landlord_id = @landlord_id,
    name = @name,
    location = @location,
    slug = @slug,
    garbage_enabled = @garbage_enabled,
    garbage_fee = @garbage_fee,
    electricity_enabled = @electricity_enabled,
    electricity_deposit_amount = @electricity_deposit_amount,
    water_rate_per_unit = @water_rate_per_unit,
    management_fee_percent = @management_fee_percent,
    payhero_channel_id = @payhero_channel_id,
    underground_capacity_units = @underground_capacity_units,
    rooftop_capacity_units = @rooftop_capacity_units,
    reconnection_fee = @reconnection_fee,
    version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND version = @version AND deleted_at IS NULL
RETURNING version;

-- ArchiveProperty is the soft delete behind DELETE /v1/properties/:id. The
-- API role has no DELETE on properties (000019); zero rows affected means
-- the property doesn't exist or is already archived.
-- name: ArchiveProperty :execrows
UPDATE properties
SET deleted_at = now(), version = version + 1
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL;

-- PropertyHasActiveLeases blocks archiving a property people still live in.
-- name: PropertyHasActiveLeases :one
SELECT EXISTS (
    SELECT 1
    FROM leases
    INNER JOIN units ON units.id = leases.unit_id
    WHERE leases.tenant_id = $1 AND units.property_id = $2 AND leases.status = 'active'
);

-- LandlordBelongsToTenant guards landlord_id on create/update. Foreign-key
-- checks run with the table owner's rights and ignore RLS, so without
-- this a manager could attach a property to another firm's landlord.
-- name: LandlordBelongsToTenant :one
SELECT EXISTS (
    SELECT 1 FROM landlords WHERE tenant_id = $1 AND id = $2
);

-- SetPropertyPrintTheme replaces the property's print theme (NULL clears
-- it), with the same optimistic version check as UpdateProperty.
-- name: SetPropertyPrintTheme :one
UPDATE properties
SET print_theme = sqlc.narg('print_theme')::jsonb,
    version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND version = @version AND deleted_at IS NULL
RETURNING version;
