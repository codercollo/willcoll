-- name: GetUnit :one
SELECT * FROM units WHERE id = $1;

-- name: ListUnitsByProperty :many
SELECT * FROM units WHERE property_id = $1 ORDER BY unit_label;

-- name: CreateUnit :one
INSERT INTO units (property_id, unit_label, unit_type, base_rent, deposit_amount, status)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateUnit :one
UPDATE units SET
    unit_label = $2,
    unit_type = $3,
    base_rent = $4,
    deposit_amount = $5,
    status = $6
WHERE id = $1
RETURNING *;

-- name: DeleteUnit :exec
DELETE FROM units WHERE id = $1;
