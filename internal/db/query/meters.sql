-- name: GetMeter :one
SELECT * FROM meters WHERE id = $1;

-- name: ListMetersByUnit :many
SELECT * FROM meters WHERE unit_id = $1;

-- name: CreateMeter :one
INSERT INTO meters (unit_id, meter_type, meter_number)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateMeter :one
UPDATE meters SET meter_type = $2, meter_number = $3
WHERE id = $1
RETURNING *;

-- name: DeleteMeter :exec
DELETE FROM meters WHERE id = $1;
