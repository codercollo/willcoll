-- name: GetProperty :one
SELECT * FROM properties WHERE id = $1;

-- name: ListProperties :many
SELECT * FROM properties ORDER BY name;

-- name: CreateProperty :one
INSERT INTO properties (
    organization_id, manager_id, name, location, brand_note,
    water_rate_per_m3, garbage_fee_flat, late_fee_pct_per_day, rent_due_day
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateProperty :one
UPDATE properties SET
    name = $2,
    location = $3,
    brand_note = $4,
    water_rate_per_m3 = $5,
    garbage_fee_flat = $6,
    late_fee_pct_per_day = $7,
    rent_due_day = $8
WHERE id = $1
RETURNING *;

-- name: DeleteProperty :exec
DELETE FROM properties WHERE id = $1;
