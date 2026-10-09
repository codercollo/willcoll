ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_system_actor_named;
ALTER TABLE audit_log DROP COLUMN IF EXISTS actor_name;
