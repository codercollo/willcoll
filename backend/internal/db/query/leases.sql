-- Leases (system-design.txt 3.2, 3.2.1). Tenant-scoped under RLS. The
-- LEASE_START deposit posting runs in the same transaction as CreateLease;
-- see data.LeaseModel.Insert and ADR 0002.

-- name: CreateLease :one
INSERT INTO leases (
    tenant_id, unit_id, tenant_name, primary_phone, rent_amount,
    rent_deposit_amount, water_deposit_amount, electricity_deposit_amount,
    start_date, end_date, status, garbage_billed
) VALUES (
    @tenant_id, @unit_id, @tenant_name, @primary_phone, @rent_amount,
    @rent_deposit_amount, @water_deposit_amount, @electricity_deposit_amount,
    @start_date, @end_date, @status, @garbage_billed
)
RETURNING *;

-- name: GetLease :one
SELECT * FROM leases
WHERE tenant_id = $1 AND id = $2;

-- ListActiveLeasesWithoutElectricityDeposit is every active lease on the
-- property that has never had an electricity deposit charged (amount still
-- zero — the same invariant the lease-start charge loop uses: zero means
-- "nothing to charge," never "waived after being charged"). Used to
-- retroactively charge existing leases the one time electricity_enabled
-- flips on for the property.
-- name: ListActiveLeasesWithoutElectricityDeposit :many
SELECT l.* FROM leases l
INNER JOIN units u ON u.id = l.unit_id
WHERE l.tenant_id = @tenant_id AND u.property_id = @property_id
  AND l.status = 'active' AND l.electricity_deposit_amount = 0
ORDER BY u.unit_code;

-- SetLeaseElectricityDeposit records the amount an existing lease was
-- retroactively charged for its electricity deposit, in the same
-- transaction as the charge itself (EnableElectricityDeposit). Not part of
-- UpdateLease: like the other deposits, this is a one-time posting, not an
-- ordinary edit.
-- name: SetLeaseElectricityDeposit :exec
UPDATE leases SET electricity_deposit_amount = @electricity_deposit_amount, version = version + 1
WHERE tenant_id = @tenant_id AND id = @id;

-- UpdateLease covers edits and termination. Deposits and start_date are
-- deliberately not updatable: they were posted to the ledger at lease
-- start, and changing them is a storno adjustment, not an edit.
-- name: UpdateLease :one
UPDATE leases
SET tenant_name = @tenant_name,
    primary_phone = @primary_phone,
    rent_amount = @rent_amount,
    end_date = @end_date,
    status = @status,
    garbage_billed = @garbage_billed,
    version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND version = @version
RETURNING version;
