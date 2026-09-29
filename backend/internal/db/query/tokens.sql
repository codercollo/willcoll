-- Tokens: activation / password-reset (system-design.txt 3.1, 4.4). Only
-- the SHA-256 hash is stored. Not tenant-scoped.

-- name: CreateToken :exec
INSERT INTO tokens (hash, manager_id, expiry, scope)
VALUES ($1, $2, $3, $4);

-- DeleteTokensForManager removes every token a manager holds, whatever its
-- scope: after a password reset, no outstanding link of any kind should
-- still work.
-- name: DeleteTokensForManager :exec
DELETE FROM tokens
WHERE manager_id = $1;

-- name: DeleteAllTokensForManager :exec
DELETE FROM tokens
WHERE scope = $1 AND manager_id = $2;
