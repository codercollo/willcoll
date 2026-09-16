-- name: GetUnitStatement :many
SELECT unit_id, period_month, rent_due, rent_paid, water_due, water_paid
FROM v_unit_statement
WHERE unit_id = $1
ORDER BY period_month;

-- name: ListUnitStatement :many
SELECT unit_id,
       period_month,
       rent_due::numeric AS rent_due,
       rent_paid::numeric AS rent_paid,
       water_due::numeric AS water_due,
       water_paid::numeric AS water_paid,
       COUNT(*) OVER()::bigint AS total_count
FROM v_unit_statement
WHERE unit_id = $1
  AND (
      $2::text <> 'landlord'
      OR EXISTS (
          SELECT 1
          FROM units u
          JOIN property_ownership po ON po.property_id = u.property_id
          WHERE u.id = v_unit_statement.unit_id
            AND po.landlord_id = $3::uuid
      )
  )
ORDER BY period_month DESC
LIMIT $4 OFFSET $5;
