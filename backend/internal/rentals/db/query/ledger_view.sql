-- Read side of the append-only ledger, plus what a reversal (storno) and a
-- payment posting need. INSERT and SELECT only on the ledger tables; see
-- ledger_entries.sql for the writes and data.TestAppendOnlyQueries.

-- ListLedgerEntriesWithBalance pages one unit ledger for display: each entry
-- with the running balance after it (over the whole ledger, oldest first),
-- its transaction type and description (a reversal reason lives there), and
-- whether a later reversal has cancelled it. Newest first unless
-- sort = 'created_at'.
-- name: ListLedgerEntriesWithBalance :many
WITH entries AS (
    SELECT
        le.id, le.transaction_header_id, le.ledger_account_id, le.direction, le.amount,
        le.reference_type, le.reference_id, le.created_at,
        h.type AS transaction_type,
        h.description,
        SUM(CASE le.direction WHEN 'DEBIT' THEN le.amount ELSE -le.amount END)
            OVER (ORDER BY le.created_at, le.id)::numeric(12,2) AS running_balance,
        EXISTS (
            SELECT 1 FROM ledger_entries r
            WHERE r.reference_type = 'reversal' AND r.reference_id = le.id
        ) AS reversed
    FROM ledger_entries le
    INNER JOIN ledger_accounts la ON la.id = le.ledger_account_id
    INNER JOIN transaction_headers h ON h.id = le.transaction_header_id
    WHERE le.tenant_id = @tenant_id AND la.unit_id = @unit_id AND la.type = @ledger_type
)
SELECT count(*) OVER() AS total_records, entries.*
FROM entries
ORDER BY
    CASE WHEN @sort::text = 'created_at' THEN entries.created_at END ASC,
    CASE WHEN @sort::text = '-created_at' THEN entries.created_at END DESC,
    entries.id ASC
LIMIT @page_limit OFFSET @page_offset;

-- GetLedgerEntryForReversal loads an entry with its account, tenant-scoped,
-- and whether it has already been reversed.
-- name: GetLedgerEntryForReversal :one
SELECT
    le.id, le.ledger_account_id, le.direction, le.amount, le.reference_type, le.reference_id,
    la.unit_id, la.type AS ledger_type,
    EXISTS (
        SELECT 1 FROM ledger_entries r
        WHERE r.reference_type = 'reversal' AND r.reference_id = le.id
    ) AS reversed
FROM ledger_entries le
INNER JOIN ledger_accounts la ON la.id = le.ledger_account_id
WHERE le.tenant_id = $1 AND le.id = $2;

-- CreatePaymentAllocation links a payment to the ledger entry that credited
-- it. The caller picks the id up front because the credit entry reference_id
-- must point back at this row: entry first, then this row. INSERT only.
-- name: CreatePaymentAllocation :exec
INSERT INTO payment_allocations (id, tenant_id, payment_id, ledger_account_id, ledger_entry_id, amount)
VALUES ($1, $2, $3, $4, $5, $6);

-- SumPaymentAllocations backs the zero-sum assertion (system-design.txt
-- 3.8): what has been allocated from one payment so far.
-- name: SumPaymentAllocations :one
SELECT COALESCE(SUM(amount), 0)::numeric(12,2) AS allocated
FROM payment_allocations
WHERE tenant_id = $1 AND payment_id = $2;

-- SetPaymentMatch records where a payment landed: its status, unit and
-- intent, whether the engine applied it on a guess a manager should confirm,
-- and a note saying why it needs review.
-- name: SetPaymentMatch :exec
UPDATE payments
SET status = @status,
    matched_unit_id = @matched_unit_id,
    matched_intent_id = @matched_intent_id,
    auto_applied_unconfirmed = @auto_applied_unconfirmed,
    review_note = @review_note
WHERE tenant_id = @tenant_id AND id = @id;

-- GetManualPaymentByReference finds an earlier hand-entered payment with the
-- same reference, so a double submit is refused instead of credited twice.
-- name: GetManualPaymentByReference :one
SELECT id FROM payments
WHERE tenant_id = $1 AND source = 'manual' AND account_reference = $2;
