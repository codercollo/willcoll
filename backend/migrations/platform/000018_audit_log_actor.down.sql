ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_id_required;
ALTER TABLE audit_log DROP COLUMN actor_id;
ALTER TABLE audit_log DROP COLUMN actor_type;
