-- Existence checks the API runs for a property-scoped caller before a route
-- that names a resource in its path. They run in the caller's tenant
-- transaction, so row-level security (tenant isolation AND the property_scope
-- policies, migration 000047) decides what exists: a row outside the caller's
-- scope is simply absent, exactly like one that was never there.

-- name: ScopeHasProperty :one
SELECT EXISTS (SELECT 1 FROM properties WHERE id = $1);

-- name: ScopeHasUnit :one
SELECT EXISTS (SELECT 1 FROM units WHERE id = $1);

-- name: ScopeHasLease :one
SELECT EXISTS (SELECT 1 FROM leases WHERE id = $1);

-- name: ScopeHasLandlord :one
SELECT EXISTS (SELECT 1 FROM landlords WHERE id = $1);

-- name: ScopeHasPayment :one
SELECT EXISTS (SELECT 1 FROM payments WHERE id = $1);

-- name: ScopeHasLedgerEntry :one
SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE id = $1);
