-- Append-only audit trail (system-design.txt A.1, A.8). Tenant-scoped
-- (RLS by manager_id, migration 000012). actor_type/actor_id (migration
-- 000018) identify who did it; staff_id is kept alongside for joins when
-- actor_type = 'staff'. ip (migration 000019) is the resolved client address.
-- actor_name (migration 000021) is the job name of a system actor.

-- name: CreateAuditLog :exec
INSERT INTO audit_log (manager_id, staff_id, actor_type, actor_id, actor_name, product, action, entity_type, entity_id, summary, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
