-- name: GetToken :one
SELECT * FROM tokens WHERE hash = $1;

-- name: ListTokensByUser :many
SELECT * FROM tokens WHERE user_id = $1 ORDER BY expiry;

-- name: CreateToken :one
INSERT INTO tokens (hash, user_id, organization_id, expiry, scope)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteToken :exec
DELETE FROM tokens WHERE hash = $1;
