-- The append-only ledger core (system-design.txt 3.3/3.4, ADR 0002).
-- transaction_headers and ledger_entries are INSERT and SELECT only: there
-- is deliberately no UPDATE or DELETE query here, the API role has no such
-- privilege (000018/000019), and data.TestAppendOnlyQueries fails if one
-- is ever added. Corrections are storno reversals.

-- GetOrCreateLedgerAccount returns the unit's anchor account of the given
-- type, creating it on first use. (ledger_accounts is not append-only; the
-- no-op DO UPDATE just makes RETURNING yield the existing row's id.)
-- name: GetOrCreateLedgerAccount :one
INSERT INTO ledger_accounts (tenant_id, unit_id, type)
VALUES ($1, $2, $3)
ON CONFLICT (unit_id, type) DO UPDATE SET type = EXCLUDED.type
RETURNING id;

-- name: CreateTransactionHeader :one
INSERT INTO transaction_headers (tenant_id, type, idempotency_key, description, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (
    tenant_id, transaction_header_id, ledger_account_id, direction, amount, reference_type, reference_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING id;

-- GetUnitLedgerBalance reads the derived balance (never a stored column).
-- The view is security_invoker (000020), so RLS applies through it; the
-- units join adds the explicit tenant filter.
-- name: GetUnitLedgerBalance :one
SELECT COALESCE((
    SELECT b.balance
    FROM unit_ledger_balances b
    INNER JOIN units u ON u.id = b.unit_id
    WHERE u.tenant_id = @tenant_id AND b.unit_id = @unit_id AND b.type = @ledger_type::text
), 0)::numeric(12,2) AS balance;

-- GetUnitLedgerBalanceAsOf is GetUnitLedgerBalance frozen at a cutoff
-- (exclusive), for telling apart genuine carried-over arrears (a balance
-- already positive before the current period's own charge was posted)
-- from a just-billed current-period charge that simply hasn't been paid
-- yet — the two look identical as an instantaneous "balance right now"
-- but only the first is arrears. Same shape as ListUnitRentBalancesAsOf,
-- for one unit instead of a whole property.
-- name: GetUnitLedgerBalanceAsOf :one
SELECT COALESCE((
    SELECT SUM(CASE le.direction WHEN 'DEBIT' THEN le.amount ELSE -le.amount END)
    FROM ledger_entries le
    INNER JOIN ledger_accounts la ON la.id = le.ledger_account_id
    WHERE la.tenant_id = @tenant_id AND la.unit_id = @unit_id AND la.type = @ledger_type::text
      AND le.created_at < @as_of::timestamptz
), 0)::numeric(12,2) AS balance;
