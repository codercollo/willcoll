-- Phase 9.1: unit statement view. This is a read-only projection over the
-- invoice read-model; no report data is stored here.

CREATE VIEW v_unit_statement AS
SELECT
    l.unit_id,
    i.period_month,
    COALESCE(SUM(i.amount_due) FILTER (WHERE i.invoice_type = 'rent'), 0) AS rent_due,
    COALESCE(SUM(i.amount_paid) FILTER (WHERE i.invoice_type = 'rent'), 0) AS rent_paid,
    COALESCE(SUM(i.amount_due) FILTER (WHERE i.invoice_type = 'water_garbage'), 0) AS water_due,
    COALESCE(SUM(i.amount_paid) FILTER (WHERE i.invoice_type = 'water_garbage'), 0) AS water_paid
FROM leases l
JOIN invoices i ON i.lease_id = l.id
GROUP BY l.unit_id, i.period_month;
