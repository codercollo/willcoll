-- name: GetInvoice :one
SELECT * FROM invoices WHERE id = $1;

-- name: ListInvoicesByLease :many
SELECT * FROM invoices WHERE lease_id = $1 ORDER BY period_month;

-- name: CreateInvoice :one
INSERT INTO invoices (
    organization_id, lease_id, period_month, invoice_type,
    amount_due, status, meter_reading_id
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateInvoice :one
UPDATE invoices SET
    amount_due = $2,
    status = $3,
    meter_reading_id = $4
WHERE id = $1
RETURNING *;

-- name: DeleteInvoice :exec
DELETE FROM invoices WHERE id = $1;
