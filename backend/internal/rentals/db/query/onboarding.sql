-- Onboarding import (migration 000030): what a property already has, so an import
-- can be checked row by row before anything is written.

-- ListUnitStatesForProperty lists a property's units with whether each has an
-- active lease, keyed for lookup by unit code.
-- name: ListUnitStatesForProperty :many
SELECT u.id, u.unit_code, u.status, (l.id IS NOT NULL)::bool AS has_active_lease
FROM units u
LEFT JOIN leases l ON l.unit_id = u.id AND l.status = 'active'
WHERE u.tenant_id = $1 AND u.property_id = $2;

-- OnboardingCounts feeds the setup checklist in one round trip.
-- name: OnboardingCounts :one
SELECT
    (SELECT count(*) FROM landlords ll WHERE ll.tenant_id = $1)::int AS landlords,
    (SELECT count(*) FROM properties p1 WHERE p1.tenant_id = $1 AND p1.deleted_at IS NULL)::int AS properties,
    (SELECT count(*) FROM properties p2 WHERE p2.tenant_id = $1 AND p2.deleted_at IS NULL AND p2.payhero_channel_id IS NOT NULL AND p2.payhero_channel_id <> '')::int AS properties_with_channel,
    (SELECT count(*) FROM units u1 JOIN properties p3 ON p3.id = u1.property_id WHERE u1.tenant_id = $1 AND p3.deleted_at IS NULL)::int AS units,
    (SELECT count(*) FROM leases l1 WHERE l1.tenant_id = $1 AND l1.status = 'active')::int AS active_leases,
    (SELECT count(*) FROM transaction_headers th WHERE th.tenant_id = $1 AND th.type = 'OPENING_BALANCE')::int AS opening_imports,
    (SELECT count(*) FROM water_readings wr WHERE wr.tenant_id = $1)::int AS water_readings,
    (SELECT count(*) FROM charges c WHERE c.tenant_id = $1 AND c.source_type = 'rent_run')::int AS rent_charges,
    (SELECT count(*) FROM unit_qr_codes q WHERE q.tenant_id = $1 AND q.revoked_at IS NULL)::int AS live_qr_codes;
