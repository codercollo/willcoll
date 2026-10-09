-- Owner-alert outbox on the Rentals side (C4.0, the transactional outbox).
--
-- An owner alert about a Rentals action (a charge reversal, a payment-channel
-- change) used to be queued in willcoll_platform.email_outbox AFTER the action
-- committed, so a crash between the two could lose it. Alerts are the
-- anti-fraud control: they must not be lost.
--
-- The two databases cannot share a transaction, so the alert is written HERE,
-- in the same transaction as the action (commit together or not at all), and a
-- relay job (cmd/rentals-api alert_relay.go) moves pending rows into the
-- platform outbox and marks them relayed. The platform enqueue is idempotent
-- per (firm, event_key), so a relay that dies between the two steps, or runs
-- twice, still sends one email.
--
--   * (tenant_id, event_key) is UNIQUE: the same event enqueued twice leaves
--     one row (a retried handler).
--   * payload is everything the email needs, captured when the action happened
--     (recipient, firm name, actor, client IP, headline, target, link, time):
--     no secrets, no raw phone numbers.
--   * status: pending -> relayed. Rows are never deleted.
--
-- RLS: products write through their own tenant transaction. The relay is
-- cross-tenant and runs on willcoll_admin (BYPASSRLS), which is granted UPDATE
-- here (000018's baseline gives it SELECT only).
CREATE TABLE IF NOT EXISTS alert_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    event_key text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'relayed')),
    attempts int NOT NULL DEFAULT 0,
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    relayed_at timestamptz NULL,
    UNIQUE (tenant_id, event_key)
);

CREATE INDEX IF NOT EXISTS alert_outbox_pending_idx ON alert_outbox (created_at) WHERE status = 'pending';

ALTER TABLE alert_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE alert_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON alert_outbox
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- The API only ever appends; settling a row is the relay's (admin) job.
REVOKE UPDATE, DELETE ON alert_outbox FROM willcoll_app;
GRANT UPDATE ON alert_outbox TO willcoll_admin;
