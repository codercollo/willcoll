-- Balance is NEVER a stored column. Indexed via
-- ledger_entries (ledger_account_id, created_at) (previous migration) so
-- this aggregates cheaply even at thousands of entries per unit.
CREATE OR REPLACE VIEW unit_ledger_balances AS
SELECT
    la.unit_id,
    la.type,
    COALESCE(SUM(
        CASE
            WHEN le.direction = 'DEBIT' THEN le.amount
            WHEN le.direction = 'CREDIT' THEN -le.amount
        END
    ), 0) AS balance
FROM ledger_accounts la
LEFT JOIN ledger_entries le ON le.ledger_account_id = la.id
GROUP BY la.unit_id, la.type;
