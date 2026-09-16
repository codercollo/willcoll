-- name: GetLedgerTransfer :one
SELECT * FROM ledger_transfers WHERE id = $1;

-- name: GetLedgerTransferByIdempotencyKey :one
SELECT * FROM ledger_transfers WHERE idempotency_key = $1;

-- name: ListLedgerTransfers :many
SELECT * FROM ledger_transfers ORDER BY created_at DESC;

-- name: CreateLedgerTransfer :one
INSERT INTO ledger_transfers (
    organization_id, transfer_type, invoice_id, method, reference,
    narrative, idempotency_key, reversed_transfer_id, recorded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;
