-- name: ListArrearsReport :many
WITH arrears AS (
    SELECT u.id AS unit_id,
           u.property_id,
           u.unit_label,
           COALESCE(SUM(i.amount_due - i.amount_paid), 0)::numeric AS arrears
    FROM units u
    JOIN leases l ON l.unit_id = u.id
    JOIN invoices i ON i.lease_id = l.id
    WHERE i.status NOT IN ('paid', 'waived')
      AND i.amount_paid < i.amount_due
    GROUP BY u.id, u.property_id, u.unit_label
)
SELECT unit_id,
       property_id,
       unit_label,
       arrears,
       COUNT(*) OVER()::bigint AS total_count
FROM arrears
WHERE (NOT $2::boolean OR property_id = $1::uuid)
  AND (
      $3::text <> 'landlord'
      OR EXISTS (
          SELECT 1
          FROM property_ownership po
          WHERE po.property_id = arrears.property_id
            AND po.landlord_id = $4::uuid
      )
  )
ORDER BY
    CASE WHEN $5::text = 'unit_label' THEN unit_label END ASC,
    CASE WHEN $5::text = '-unit_label' THEN unit_label END DESC,
    CASE WHEN $5::text = 'arrears' THEN arrears END ASC,
    CASE WHEN $5::text = '-arrears' THEN arrears END DESC,
    property_id ASC,
    unit_label ASC
LIMIT $6 OFFSET $7;

-- name: ListCollectionsReport :many
WITH collections AS (
    SELECT u.property_id,
           COALESCE(SUM(i.amount_paid), 0)::numeric AS collected
    FROM invoices i
    JOIN leases l ON l.id = i.lease_id
    JOIN units u ON u.id = l.unit_id
    WHERE i.period_month = $1::date
    GROUP BY u.property_id
)
SELECT property_id,
       collected,
       COUNT(*) OVER()::bigint AS total_count
FROM collections
WHERE (NOT $3::boolean OR property_id = $2::uuid)
  AND (
      $4::text <> 'landlord'
      OR EXISTS (
          SELECT 1
          FROM property_ownership po
          WHERE po.property_id = collections.property_id
            AND po.landlord_id = $5::uuid
      )
  )
ORDER BY
    CASE WHEN $6::text = 'property_id' THEN property_id END ASC,
    CASE WHEN $6::text = '-property_id' THEN property_id END DESC,
    CASE WHEN $6::text = 'collected' THEN collected END ASC,
    CASE WHEN $6::text = '-collected' THEN collected END DESC,
    property_id ASC
LIMIT $7 OFFSET $8;

-- name: ListPortfolioReport :many
WITH portfolio AS (
    SELECT p.id AS property_id,
           p.name,
           COUNT(u.id)::bigint AS units
    FROM properties p
    LEFT JOIN units u ON u.property_id = p.id
    WHERE (
        $1::text <> 'landlord'
        OR EXISTS (
            SELECT 1
            FROM property_ownership po
            WHERE po.property_id = p.id
              AND po.landlord_id = $2::uuid
        )
    )
      AND ($3::text = '' OR p.name ILIKE '%' || $3::text || '%')
    GROUP BY p.id, p.name
)
SELECT property_id,
       name,
       units,
       COUNT(*) OVER()::bigint AS total_count
FROM portfolio
ORDER BY
    CASE WHEN $4::text = 'name' THEN name END ASC,
    CASE WHEN $4::text = '-name' THEN name END DESC,
    CASE WHEN $4::text = 'units' THEN units END ASC,
    CASE WHEN $4::text = '-units' THEN units END DESC,
    name ASC,
    property_id ASC
LIMIT $5 OFFSET $6;
