-- name: GetAuditLogEntry :one
SELECT * FROM audit_log WHERE id = $1;

-- name: ListAuditLog :many
SELECT * FROM audit_log ORDER BY created_at DESC;

-- name: ListAuditLogByProperty :many
SELECT * FROM audit_log WHERE property_id = $1 ORDER BY created_at DESC;

-- name: CreateAuditLogEntry :one
INSERT INTO audit_log (
    organization_id, property_id, actor_id, action, entity_type, entity_id, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;
