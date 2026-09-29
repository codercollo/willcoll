-- Payment ingestion (system-design.txt 4.8, 5).
--
-- The first three queries are CROSS-TENANT lookups: an inbound webhook does
-- not know its tenant yet, so they run on the willcoll_admin pool (BYPASSRLS,
-- SELECT only), return only ids, and are called only by data.ResolverModel.
-- Everything after them is tenant-scoped under RLS.

-- ResolveIntentByReference finds the tenant and intent an STK reference
-- belongs to.
-- name: ResolveIntentByReference :one
SELECT id, tenant_id, unit_id
FROM payment_intents
WHERE external_reference = $1;

-- ResolveChannelTenants finds the manager(s) whose properties use a PayHero
-- collections channel. More than one row means the channel is ambiguous and
-- the payment cannot be routed.
-- name: ResolveChannelTenants :many
SELECT DISTINCT tenant_id
FROM properties
WHERE payhero_channel_id = $1 AND deleted_at IS NULL;

-- ResolvePayTarget maps the public pay URL to its tenant, property and unit.
-- name: ResolvePayTarget :one
SELECT p.tenant_id, p.id AS property_id, u.id AS unit_id
FROM properties p
INNER JOIN units u ON u.property_id = p.id
WHERE p.slug = $1 AND u.unit_code = $2 AND p.deleted_at IS NULL;

-- GetPaymentIntentByReference is the tenant-scoped intent lookup for a webhook
-- whose tenant is now known.
-- name: GetPaymentIntentByReference :one
SELECT * FROM payment_intents
WHERE tenant_id = $1 AND external_reference = $2;

-- SetPaymentIntentStatus moves an intent to completed, expired or failed.
-- name: SetPaymentIntentStatus :exec
UPDATE payment_intents
SET status = $3, failure_reason = $4
WHERE tenant_id = $1 AND id = $2;

-- ListPayerCandidates is every payer identity (phone or name) on the active
-- leases of units in the manager properties that use a PayHero channel.
-- name: ListPayerCandidates :many
SELECT u.id AS unit_id, 'phone'::text AS kind, l.primary_phone AS value
FROM leases l
INNER JOIN units u ON u.id = l.unit_id
INNER JOIN properties p ON p.id = u.property_id
WHERE l.tenant_id = @tenant_id AND l.status = 'active'
  AND p.payhero_channel_id = @channel_id AND p.deleted_at IS NULL
UNION ALL
SELECT u.id, 'name', l.tenant_name
FROM leases l
INNER JOIN units u ON u.id = l.unit_id
INNER JOIN properties p ON p.id = u.property_id
WHERE l.tenant_id = @tenant_id AND l.status = 'active'
  AND p.payhero_channel_id = @channel_id AND p.deleted_at IS NULL
UNION ALL
SELECT u.id, 'phone', lp.phone
FROM lease_payers lp
INNER JOIN leases l ON l.id = lp.lease_id AND l.status = 'active'
INNER JOIN units u ON u.id = l.unit_id
INNER JOIN properties p ON p.id = u.property_id
WHERE lp.tenant_id = @tenant_id AND lp.phone IS NOT NULL
  AND p.payhero_channel_id = @channel_id AND p.deleted_at IS NULL
UNION ALL
SELECT u.id, 'name', lp.name
FROM lease_payers lp
INNER JOIN leases l ON l.id = lp.lease_id AND l.status = 'active'
INNER JOIN units u ON u.id = l.unit_id
INNER JOIN properties p ON p.id = u.property_id
WHERE lp.tenant_id = @tenant_id
  AND p.payhero_channel_id = @channel_id AND p.deleted_at IS NULL;

-- ListUnitBalances is every derived ledger balance of one unit.
-- name: ListUnitBalances :many
SELECT b.type, b.balance::numeric(12,2) AS balance
FROM unit_ledger_balances b
INNER JOIN units u ON u.id = b.unit_id
WHERE u.tenant_id = $1 AND b.unit_id = $2;
