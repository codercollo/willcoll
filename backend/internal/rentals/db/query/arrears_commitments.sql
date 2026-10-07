-- Commitment letters: the one piece of NOTE:2 that is a manager's record
-- of a real paper document, not derived from the ledger.

-- name: ListArrearsCommitments :many
SELECT ac.unit_id, u.unit_code, ac.note, ac.created_at
FROM arrears_commitments ac
INNER JOIN units u ON u.id = ac.unit_id
WHERE ac.tenant_id = @tenant_id AND u.property_id = @property_id AND ac.period = @period
ORDER BY u.unit_code;

-- name: CreateArrearsCommitment :one
INSERT INTO arrears_commitments (tenant_id, unit_id, period, note, created_by)
VALUES (@tenant_id, @unit_id, @period, @note, @created_by)
RETURNING id, created_at;
