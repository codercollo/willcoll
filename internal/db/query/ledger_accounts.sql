-- name: GetLedgerAccount :one
SELECT * FROM ledger_accounts WHERE id = $1;

-- name: ListLedgerAccounts :many
SELECT * FROM ledger_accounts ORDER BY created_at;

-- name: CreateLedgerAccount :one
INSERT INTO ledger_accounts (organization_id, owner_type, owner_id, currency, balance)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateLedgerAccountBalance :one
UPDATE ledger_accounts SET balance = $2 WHERE id = $1
RETURNING *;

-- name: DeleteLedgerAccount :exec
DELETE FROM ledger_accounts WHERE id = $1;
