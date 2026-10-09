-- Money received on a PayHero channel that has no claim in payment_channels
-- (C3.2: the posting product and firm come only from a claim, so nothing is
-- posted). A log line is not an acceptable record of received money, so every
-- such payment is written here, durably, and stays 'open' on the Super Admin
-- System page until somebody resolves it with a note.
--
--   * mpesa_receipt is UNIQUE: the webhook writes idempotently, so PayHero's
--     own redelivery (or a replay) leaves one row.
--   * channel_id has NO foreign key to payment_channels: the whole point is
--     that the channel is not there.
--   * msisdn and raw_payload are stored in full, so the payment can be traced
--     and replayed once the channel is claimed; the UI masks the phone.
--   * resolved_by is the Super Admin who closed it (no FK: admins are not
--     tenants and the row must outlive any change to them); the note says how.
--
-- No tenant column, so no RLS (same precedent as payment_inbox: a webhook
-- arrives with no tenant context). The app role may only INSERT and read: a
-- received-money record is never edited or deleted by a product API. Only the
-- admin role resolves rows.
CREATE TABLE IF NOT EXISTS unclaimed_receipts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mpesa_receipt text NOT NULL UNIQUE,
    channel_id text NOT NULL,
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste', 'platform')),
    amount numeric(12,2) NOT NULL,
    msisdn text NOT NULL,
    raw_payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    resolved_by uuid NULL,
    resolved_at timestamptz NULL,
    resolution_note text NULL,
    CONSTRAINT unclaimed_receipts_resolution_complete CHECK (
        (status = 'open' AND resolved_by IS NULL AND resolved_at IS NULL AND resolution_note IS NULL)
        OR (status = 'resolved' AND resolved_by IS NOT NULL AND resolved_at IS NOT NULL AND length(btrim(resolution_note)) > 0)
    )
);

-- The System page lists exactly the open rows.
CREATE INDEX unclaimed_receipts_open_idx ON unclaimed_receipts (received_at) WHERE status = 'open';

REVOKE UPDATE, DELETE ON unclaimed_receipts FROM willcoll_platform_app;
GRANT UPDATE ON unclaimed_receipts TO willcoll_platform_admin;
