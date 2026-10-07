-- Electricity deposit (Feature 2): a one-off refundable deposit, not a
-- monthly bill, so unlike garbage/water there is no "run" — just the
-- lease-start (or EnableElectricityDeposit) charge and whatever the tenant
-- has paid down since.

-- ListElectricityDeposits is every occupied unit's electricity deposit
-- line for the property tab: same shape as ListGarbageUnitsForPeriod (all
-- occupied units, not just the ones with a deposit — a unit with none
-- shows zero required, matching "Not billed" for garbage).
-- name: ListElectricityDeposits :many
SELECT
    u.id AS unit_id,
    u.unit_code,
    COALESCE(l.tenant_name, '')::text AS tenant_name,
    COALESCE(l.electricity_deposit_amount, 0)::numeric(12,2) AS deposit_required,
    COALESCE(b.balance, 0)::numeric(12,2) AS balance
FROM units u
LEFT JOIN LATERAL (
    SELECT tenant_name, electricity_deposit_amount FROM leases
    WHERE unit_id = u.id AND status = 'active'
    LIMIT 1
) l ON true
LEFT JOIN LATERAL (
    SELECT ub.balance FROM unit_ledger_balances ub
    WHERE ub.unit_id = u.id AND ub.type = 'ELECTRICITY_DEPOSIT'
) b ON true
WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id AND u.status = 'occupied'
ORDER BY u.unit_code;
