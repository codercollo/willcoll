-- The public pay page (system-design.txt 4.7): who may pay for a unit, the
-- one-time codes that prove it, and the payer-facing view of the unit.
-- Tenant-scoped under RLS: the caller resolves the manager first.

-- ListUnitPayPhones is every phone on the unit active lease: the tenant and
-- any co-payers. Only these numbers may request a code.
-- name: ListUnitPayPhones :many
SELECT l.primary_phone AS phone
FROM leases l
WHERE l.tenant_id = $1 AND l.unit_id = $2 AND l.status = 'active'
UNION
SELECT lp.phone
FROM lease_payers lp
INNER JOIN leases l ON l.id = lp.lease_id
WHERE lp.tenant_id = $1 AND l.unit_id = $2 AND l.status = 'active' AND lp.phone IS NOT NULL;

-- name: CountRecentPayOTPs :one
SELECT count(*)::int AS n
FROM pay_otps
WHERE tenant_id = $1 AND unit_id = $2 AND phone = $3 AND created_at > $4;

-- name: CreatePayOTP :exec
INSERT INTO pay_otps (tenant_id, unit_id, phone, code_hash, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetLatestPayOTP :one
SELECT * FROM pay_otps
WHERE tenant_id = $1 AND unit_id = $2 AND phone = $3
ORDER BY created_at DESC
LIMIT 1;

-- name: BumpPayOTPAttempts :exec
UPDATE pay_otps SET attempts = attempts + 1 WHERE tenant_id = $1 AND id = $2;

-- ConsumePayOTP marks a code used; zero rows means it already was.
-- name: ConsumePayOTP :execrows
UPDATE pay_otps SET consumed_at = now()
WHERE tenant_id = $1 AND id = $2 AND consumed_at IS NULL;

-- name: PurgePayOTPs :exec
DELETE FROM pay_otps WHERE tenant_id = $1 AND created_at < $2;
