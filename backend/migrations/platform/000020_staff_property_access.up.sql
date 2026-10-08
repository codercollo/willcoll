-- Property scope for staff (docs/security/authorization.md section 3, D2).
-- Keyed by product, valued by a list of property ids:
--   {"rentals": ["<property uuid>", ...]}
-- Properties live in the product databases, so there is no foreign key; ids
-- that no longer exist match nothing (pruned lazily). A missing key or an
-- empty list means ALL properties of that product, so every existing staff
-- row keeps exactly today's access. The firm owner login and staff owners are
-- never restricted (enforced when the value is written and when it is read).
ALTER TABLE staff ADD COLUMN property_access jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE staff ADD CONSTRAINT staff_property_access_is_object
    CHECK (jsonb_typeof(property_access) = 'object');
