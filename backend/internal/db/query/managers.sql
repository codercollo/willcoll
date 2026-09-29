-- Managers: the SaaS tenant (system-design.txt 3.1). Not tenant-scoped (no
-- RLS), so these run on the Store directly, outside ExecTenantTx.

-- name: CreateManager :one
INSERT INTO managers (firm_name, username, email, phone, password_hash, status)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetManager :one
SELECT * FROM managers
WHERE id = $1;

-- name: GetManagerByEmail :one
SELECT * FROM managers
WHERE email = $1;

-- GetManagerForToken finds the manager a still-valid token of the given
-- scope belongs to. The caller passes the token's SHA-256 hash.
-- name: GetManagerForToken :one
SELECT managers.*
FROM managers
INNER JOIN tokens ON tokens.manager_id = managers.id
WHERE tokens.hash = @hash AND tokens.scope = @scope AND tokens.expiry > now();

-- SetManagerStatus moves a manager from one status to another: the Super
-- Admin's suspend and reinstate (/v1/admin/managers/:id/suspend|reinstate).
-- The from_status guard makes each move explicit: zero rows means the
-- manager doesn't exist or wasn't in from_status.
-- name: SetManagerStatus :one
UPDATE managers
SET status = @to_status, version = version + 1
WHERE id = @id AND status = @from_status
RETURNING version;

-- UpdateManager: optimistic concurrency; zero rows means the version moved on.
-- name: UpdateManager :one
UPDATE managers
SET firm_name = @firm_name,
    username = @username,
    email = @email,
    phone = @phone,
    password_hash = @password_hash,
    status = @status,
    activated_at = @activated_at,
    trial_ends_at = @trial_ends_at,
    version = version + 1
WHERE id = @id AND version = @version
RETURNING version;
