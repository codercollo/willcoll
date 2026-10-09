-- payment_channels: every PayHero channel belongs to exactly one product and
-- owner (system-design.txt A.6, build-phases.txt 3g-1). channel_id is the
-- primary key, so a second registration of the same channel for a different
-- owner is refused by the database itself (23505 on the PK) — the caller
-- maps that into a 409. Tenant-scoped (RLS by manager_id).

-- RegisterPaymentChannel claims channel_id for (product, owner_id) under
-- manager_id. Plain INSERT, no ON CONFLICT: a conflict here is a real
-- refusal (the channel already belongs to someone), never something to
-- silently absorb.
-- name: RegisterPaymentChannel :one
INSERT INTO payment_channels (channel_id, manager_id, product, owner_id, callback_url)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- GetPaymentChannelByOwner backs the backfill's idempotency: a property
-- already registered is skipped, never re-inserted.
-- name: GetPaymentChannelByOwner :one
SELECT * FROM payment_channels
WHERE manager_id = $1 AND product = $2 AND owner_id = $3;

-- GetPaymentChannel resolves a webhook's channel_id to its owner, inside the
-- claiming product's own tenant transaction (manager_id known from the claim
-- step already, so RLS still applies).
-- name: GetPaymentChannel :one
SELECT * FROM payment_channels
WHERE manager_id = $1 AND channel_id = $2;

-- ReleasePaymentChannel drops (product, owner_id)'s claim on channel_id —
-- used when a property's channel is changed or cleared (build-phases.txt
-- 3i-d). Scoped to the exact owner so one property can never release a
-- channel another owns. Fails with a foreign-key violation if payment_inbox
-- already holds receipts for the channel; the caller keeps the claim then.
-- name: ReleasePaymentChannel :execrows
DELETE FROM payment_channels
WHERE manager_id = $1 AND channel_id = $2 AND product = $3 AND owner_id = $4;

-- GetPaymentChannelByID resolves a webhook's channel_id to its claim with no
-- tenant context (a webhook names a channel, not a manager), so it runs on
-- the admin pool (BYPASSRLS). The claim, never the webhook payload, says
-- which product and firm a payment belongs to (C3.2).
-- name: GetPaymentChannelByID :one
SELECT * FROM payment_channels WHERE channel_id = $1;
