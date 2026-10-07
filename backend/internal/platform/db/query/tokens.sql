-- Tokens: activation / password-reset (system-design.txt A.1). Only the
-- SHA-256 hash is stored. Belongs to exactly one manager or one staff row
-- (000004's exactly-one-owner check); pass the other as NULL. Not
-- tenant-scoped in the RLS sense at the call site — these run on the
-- Store directly, same as the rest of identity.

-- name: CreateToken :exec
INSERT INTO tokens (hash, manager_id, staff_id, expiry, scope)
VALUES ($1, $2, $3, $4, $5);

-- DeleteTokensForManager removes every token a manager holds directly,
-- whatever its scope: after a password reset, no outstanding link of any
-- kind should still work.
-- name: DeleteTokensForManager :exec
DELETE FROM tokens
WHERE manager_id = $1;

-- name: DeleteAllTokensForManager :exec
DELETE FROM tokens
WHERE scope = $1 AND manager_id = $2;

-- name: DeleteAllTokensForStaff :exec
DELETE FROM tokens
WHERE scope = $1 AND staff_id = $2;
