-- Landlords (system-design.txt 1.3, 3.2). Tenant-scoped under RLS; every
-- query also filters on tenant_id.

-- name: CreateLandlord :one
INSERT INTO landlords (
    tenant_id, name, phone, email, bank_name, bank_account_name, bank_account_number
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetLandlord :one
SELECT * FROM landlords
WHERE tenant_id = $1 AND id = $2;

-- ListLandlords pages a tenant's landlords. The sort key comes from the
-- handler's safelist (see ListProperties for the CASE-ordering idiom).
-- name: ListLandlords :many
SELECT count(*) OVER() AS total_records, sqlc.embed(landlords)
FROM landlords
WHERE tenant_id = @tenant_id
ORDER BY
    CASE WHEN @sort::text = 'name' THEN name END ASC,
    CASE WHEN @sort::text = '-name' THEN name END DESC,
    CASE WHEN @sort::text = 'created_at' THEN created_at END ASC,
    CASE WHEN @sort::text = '-created_at' THEN created_at END DESC,
    id ASC
LIMIT @page_limit OFFSET @page_offset;

-- UpdateLandlord: optimistic concurrency; zero rows means the version moved on.
-- name: UpdateLandlord :one
UPDATE landlords
SET name = @name,
    phone = @phone,
    email = @email,
    bank_name = @bank_name,
    bank_account_name = @bank_account_name,
    bank_account_number = @bank_account_number,
    version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND version = @version
RETURNING version;
