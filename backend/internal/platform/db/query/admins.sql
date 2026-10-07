-- Super Admin (system-design.txt A.1). One row, platform-wide, outside the
-- tenant boundary — these run on the API's willcoll_platform_app pool.

-- name: GetAdmin :one
SELECT * FROM admins
WHERE id = $1;

-- name: GetAdminByEmail :one
SELECT * FROM admins
WHERE email = $1;

-- UpsertAdmin provisions the one Super Admin from configuration at
-- startup. It conflicts on is_singleton (000001), so there is only ever
-- one row: a changed ADMIN_EMAIL updates it in place, keeping its id.
-- name: UpsertAdmin :one
INSERT INTO admins (name, email, password_hash)
VALUES ($1, $2, $3)
ON CONFLICT (is_singleton) DO UPDATE
SET name = EXCLUDED.name, email = EXCLUDED.email, password_hash = EXCLUDED.password_hash
RETURNING *;
