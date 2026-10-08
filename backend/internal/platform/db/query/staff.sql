-- Staff: sub-accounts under a manager (system-design.txt A.1,
-- build-phases.txt 3f-1). Tenant-scoped (RLS by manager_id, migration
-- 000012) for owner-driven management; the login/activation lookups below
-- are cross-tenant (no tenant context exists yet) and run on
-- willcoll_platform_admin (BYPASSRLS) instead — see StaffModel.

-- name: CreateStaff :one
INSERT INTO staff (manager_id, name, email, phone, password_hash, role, module_access, status, property_access)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetStaff :one
SELECT * FROM staff WHERE id = $1;

-- name: ListStaffForManager :many
SELECT * FROM staff WHERE manager_id = $1 ORDER BY created_at;

-- ListActiveStaffByEmail resolves a login attempt: staff.email is only
-- unique per manager, so a global lookup can return more than one ACTIVE
-- row (a person invited under two different managers with the same email —
-- rare, but possible; an invited/disabled row is never a login candidate).
-- The caller treats anything but exactly one match as "not found," never
-- guessing which account.
-- name: ListActiveStaffByEmail :many
SELECT * FROM staff WHERE email = $1 AND status = 'active';

-- name: GetStaffForToken :one
SELECT staff.* FROM staff
INNER JOIN tokens ON tokens.staff_id = staff.id
WHERE tokens.hash = @hash AND tokens.scope = @scope AND tokens.expiry > now();

-- UpdateStaff: optimistic concurrency. Used both for an owner's edits
-- (role, module_access, property_access, name, phone, status) and for self-activation
-- (password_hash + status -> active) from an invite token.
-- name: UpdateStaff :one
UPDATE staff
SET name = @name, email = @email, phone = @phone, password_hash = @password_hash,
    role = @role, module_access = @module_access, status = @status,
    property_access = @property_access, version = version + 1
WHERE id = @id AND version = @version
RETURNING version;
