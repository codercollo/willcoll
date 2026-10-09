-- The Super Admin's append-only trail (migration 000026). No RLS: the table is
-- not tenant data. manager_id is the firm an action concerned, when it did.

-- name: CreateAdminAuditLog :exec
INSERT INTO admin_audit_log (admin_id, manager_id, action, entity_type, entity_id, summary, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAdminAuditLog :many
SELECT id, admin_id, manager_id, action, entity_type, entity_id, summary, ip, created_at
FROM admin_audit_log
ORDER BY created_at DESC, id
LIMIT $1;
