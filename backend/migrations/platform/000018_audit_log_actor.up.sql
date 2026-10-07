-- Record the actor properly (build-phases.txt 3f-4 audit sweep): staff_id
-- alone left the manager's own action reading as blank. actor_type tells
-- who did it (manager|staff|admin|system); actor_id is that principal's
-- id, NULL only for a system job. staff_id is kept (FK to staff, ON
-- DELETE SET NULL) for joins, but is no longer the only signal.
ALTER TABLE audit_log ADD COLUMN actor_type text NOT NULL DEFAULT 'manager'
    CHECK (actor_type IN ('manager', 'staff', 'admin', 'system'));
ALTER TABLE audit_log ADD COLUMN actor_id uuid NULL;

-- Backfill: every existing row was either the manager's own action, or a
-- staff member's (staff_id set).
UPDATE audit_log
SET actor_type = CASE WHEN staff_id IS NOT NULL THEN 'staff' ELSE 'manager' END,
    actor_id = COALESCE(staff_id, manager_id);

ALTER TABLE audit_log ALTER COLUMN actor_type DROP DEFAULT;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_id_required
    CHECK (actor_type = 'system' OR actor_id IS NOT NULL);
