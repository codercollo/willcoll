-- Payment intents and inbound payments (system-design.txt 3.5, 3.8).
-- Tenant-scoped under RLS.

-- name: CreatePaymentIntent :one
INSERT INTO payment_intents (tenant_id, unit_id, lines, external_reference, phone, status, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetPaymentIntent :one
SELECT * FROM payment_intents
WHERE tenant_id = $1 AND id = $2;

-- A tenant who resubmits the same payment after an ambiguous PayHero
-- timeout (the STK push may already be out) must get back the intent
-- already created for it, not a fresh one with a new reference — otherwise
-- a second prompt to the same phone risks a double charge.
-- name: GetPendingIntentForUnit :one
SELECT * FROM payment_intents
WHERE tenant_id = $1 AND unit_id = $2 AND status = $3 AND expires_at > $4 AND lines = $5
ORDER BY created_at DESC
LIMIT 1;

-- CreatePayment is idempotent on mpesa_receipt: a PayHero retry of the
-- same delivery inserts nothing and returns no row (sql.ErrNoRows).
-- matched_unit_id is set up front only when the unit is known at insert (a
-- payment a staff member records against a unit): a property-scoped caller can
-- only see, and so only insert, a payment that already names an in-scope unit
-- (migration 000047). The webhook paths leave it NULL and match afterwards.
-- A payment a caretaker records waits as 'pending' (migration 000051): the
-- ledger it will be credited to, and who and in what role recorded it, are
-- stored with it and stay NULL on every webhook-delivered payment.
-- name: CreatePayment :one
INSERT INTO payments (
    tenant_id, source, mpesa_receipt, payhero_reference, amount, msisdn,
    payer_name, account_reference, status, raw_payload, received_at, matched_unit_id,
    pending_ledger_type, recorded_by, recorded_by_role
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, sqlc.narg('matched_unit_id'),
    sqlc.narg('pending_ledger_type'), sqlc.narg('recorded_by'), sqlc.narg('recorded_by_role')
)
ON CONFLICT (mpesa_receipt) DO NOTHING
RETURNING *;

-- name: GetPaymentByMpesaReceipt :one
SELECT * FROM payments
WHERE tenant_id = $1 AND mpesa_receipt = $2;
