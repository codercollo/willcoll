-- Lease-bound unit QR codes (migration 000028). Tenant-scoped under RLS except
-- ResolveUnitQRToken, which runs on the willcoll_admin pool (SELECT only)
-- because the public scan endpoint has no tenant yet.

-- name: CreateUnitQRCode :one
INSERT INTO unit_qr_codes (token, lease_id, unit_id, property_id, tenant_id, created_by)
VALUES (@token, @lease_id, @unit_id, @property_id, @tenant_id, @created_by)
RETURNING *;

-- GetActiveUnitQRCodeByLease is the lease's live code, if it has one.
-- name: GetActiveUnitQRCodeByLease :one
SELECT * FROM unit_qr_codes
WHERE tenant_id = $1 AND lease_id = $2 AND revoked_at IS NULL;

-- name: GetUnitQRCodeByToken :one
SELECT * FROM unit_qr_codes
WHERE tenant_id = $1 AND token = $2;

-- RevokeUnitQRCodesForLease retires the lease's live code. Zero rows means it
-- had none (already revoked, or never generated), which is fine.
-- name: RevokeUnitQRCodesForLease :execrows
UPDATE unit_qr_codes
SET revoked_at = now()
WHERE tenant_id = $1 AND lease_id = $2 AND revoked_at IS NULL;

-- IncrementUnitQRScan counts one scan of a live code. Zero rows means the
-- code was revoked between the lookup and now.
-- name: IncrementUnitQRScan :execrows
UPDATE unit_qr_codes
SET scan_count = scan_count + 1, last_scanned_at = now()
WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL;

-- ResolveUnitQRToken is the public scan lookup: token to the unit's pay URL
-- parts, plus whether the code and its lease are still live. It joins only what
-- a redirect needs and returns nothing about the tenant.
-- name: ResolveUnitQRToken :one
SELECT q.id, q.tenant_id, p.slug AS property_slug, u.unit_code,
       (q.revoked_at IS NULL AND l.status = 'active' AND p.deleted_at IS NULL) AS active
FROM unit_qr_codes q
INNER JOIN leases l ON l.id = q.lease_id
INNER JOIN units u ON u.id = q.unit_id
INNER JOIN properties p ON p.id = q.property_id
WHERE q.token = $1;
