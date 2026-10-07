-- Charges: WHAT IS OWED (system-design.txt 3.3). Append-only: INSERT and
-- SELECT only. A wrong charge is corrected by a negative-amount 'manual'
-- charge (storno), never an UPDATE or DELETE.

-- name: CreateCharge :one
INSERT INTO charges (
    tenant_id, ledger_account_id, source_type, source_id, period, amount, description, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id;
