-- The Properties card grid (system-design.txt 6.1, 10): per property, who
-- owns it, how many units are occupied and vacant, and how much rent has been
-- collected against what was expected this period. Tenant-scoped under RLS.

-- ListPropertySummaries has one row per live property of the manager.
-- Expected is what the occupied units owe for the period (the billed snapshot
-- if the month has been run, else the lease current rent). Collected is rent
-- received in the period in Kenya calendar dates, ignoring payments since
-- reversed; it counts every unit, since a payment can arrive after a move-out.
-- name: ListPropertySummaries :many
SELECT
    p.id AS property_id,
    ll.name AS landlord_name,
    (count(u.id) FILTER (WHERE u.status = 'occupied'))::int AS occupied,
    (count(u.id) FILTER (WHERE u.id IS NOT NULL AND u.status <> 'occupied'))::int AS vacant,
    COALESCE(SUM(COALESCE(rr.amount_snapshot, l.rent_amount)) FILTER (WHERE u.status = 'occupied'), 0)::numeric(12,2) AS expected,
    COALESCE(SUM(paid.amount), 0)::numeric(12,2) AS collected
FROM properties p
INNER JOIN landlords ll ON ll.id = p.landlord_id
LEFT JOIN units u ON u.property_id = p.id
LEFT JOIN leases l ON l.unit_id = u.id AND l.status = 'active'
LEFT JOIN rent_runs rr ON rr.unit_id = u.id AND rr.period = sqlc.arg(period)
LEFT JOIN LATERAL (
    SELECT SUM(pa.amount) AS amount
    FROM payment_allocations pa
    INNER JOIN payments py ON py.id = pa.payment_id
    INNER JOIN ledger_accounts la ON la.id = pa.ledger_account_id
    WHERE la.unit_id = u.id
      AND la.type = 'RENT'
      AND (py.received_at AT TIME ZONE 'Africa/Nairobi')::date >= sqlc.arg(period)::date
      AND (py.received_at AT TIME ZONE 'Africa/Nairobi')::date < (sqlc.arg(period)::date + interval '1 month')
      AND NOT EXISTS (
          SELECT 1 FROM ledger_entries r
          WHERE r.reference_type = 'reversal' AND r.reference_id = pa.ledger_entry_id
      )
) paid ON true
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
GROUP BY p.id, ll.name;
