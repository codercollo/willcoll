-- Backfill for migration 000033: garbage_billed defaulted to false for
-- every existing lease, which silently turned off garbage billing for
-- properties that were already billing it. For any property with
-- garbage_enabled = true, turn garbage_billed on for every currently
-- active lease, matching the property-wide behavior that existed before
-- the per-lease toggle was introduced.
UPDATE leases l
SET garbage_billed = true
FROM units u, properties p
WHERE l.unit_id = u.id
  AND u.property_id = p.id
  AND l.status = 'active'
  AND p.garbage_enabled = true
  AND l.garbage_billed = false;
