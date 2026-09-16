-- name: GetLedgerEntry :one
SELECT * FROM ledger_entries WHERE id = $1;

-- name: ListLedgerEntriesByAccount :many
SELECT * FROM ledger_entries WHERE account_id = $1 ORDER BY created_at;

-- name: ListLedgerEntriesByTransfer :many
SELECT * FROM ledger_entries WHERE transfer_id = $1 ORDER BY created_at;

-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (account_id, amount, transfer_id)
VALUES ($1, $2, $3)
RETURNING *;
