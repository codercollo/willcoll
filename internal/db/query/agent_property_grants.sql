-- name: GetAgentPropertyGrant :one
SELECT * FROM agent_property_grants WHERE id = $1;

-- name: ListAgentPropertyGrantsByAgent :many
SELECT * FROM agent_property_grants WHERE agent_id = $1;

-- name: ListAgentPropertyGrantsByProperty :many
SELECT * FROM agent_property_grants WHERE property_id = $1;

-- name: CreateAgentPropertyGrant :one
INSERT INTO agent_property_grants (
    agent_id, property_id, can_record_payments, can_edit_leases,
    can_edit_unit_pricing, can_void_payments, can_view_financial_reports,
    can_manage_meter_readings, granted_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateAgentPropertyGrant :one
UPDATE agent_property_grants SET
    can_record_payments = $2,
    can_edit_leases = $3,
    can_edit_unit_pricing = $4,
    can_void_payments = $5,
    can_view_financial_reports = $6,
    can_manage_meter_readings = $7,
    revoked_at = $8
WHERE id = $1
RETURNING *;

-- name: DeleteAgentPropertyGrant :exec
DELETE FROM agent_property_grants WHERE id = $1;
