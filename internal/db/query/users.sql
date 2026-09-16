-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: ListUsersByOrganization :many
SELECT * FROM users WHERE organization_id = $1 ORDER BY full_name;

-- name: CreateUser :one
INSERT INTO users (
    organization_id, full_name, phone, email, password_hash,
    role, is_super_manager, activated, status, invited_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateUser :one
UPDATE users SET
    full_name = $2,
    phone = $3,
    email = $4,
    password_hash = $5,
    role = $6,
    is_super_manager = $7,
    activated = $8,
    status = $9,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
