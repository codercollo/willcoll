-- Staff status gains 'invited' (created but never activated) and
-- 'disabled' (build-phases.txt 3f-1): the original CHECK only had
-- active/suspended, from before the invite flow was designed. 'suspended'
-- is renamed to 'disabled' for consistency with the invite/activate/disable
-- lifecycle language used elsewhere in this migration set.
ALTER TABLE staff DROP CONSTRAINT staff_status_check;
UPDATE staff SET status = 'disabled' WHERE status = 'suspended';
ALTER TABLE staff ADD CONSTRAINT staff_status_check CHECK (status IN ('invited', 'active', 'disabled'));
ALTER TABLE staff ALTER COLUMN status SET DEFAULT 'invited';
