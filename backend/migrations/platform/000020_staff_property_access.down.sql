ALTER TABLE staff DROP CONSTRAINT IF EXISTS staff_property_access_is_object;
ALTER TABLE staff DROP COLUMN IF EXISTS property_access;
