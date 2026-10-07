ALTER TABLE staff ALTER COLUMN status SET DEFAULT 'active';
ALTER TABLE staff DROP CONSTRAINT staff_status_check;
UPDATE staff SET status = 'suspended' WHERE status = 'disabled';
UPDATE staff SET status = 'active' WHERE status = 'invited';
ALTER TABLE staff ADD CONSTRAINT staff_status_check CHECK (status IN ('active', 'suspended'));
