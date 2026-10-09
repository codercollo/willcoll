-- Owner security alerts (C3.7) are queued here and delivered by platform-api's
-- outbox worker, so a mail outage can never fail or lose the action that
-- raised them, and a restart cannot drop an unsent alert.
--
--   * event_key + manager_id is UNIQUE: enqueueing the same event twice
--     (a retried handler, a replayed request) leaves one row.
--   * payload is the template data, already rendered to plain strings (no
--     secrets, phone numbers masked, times in Africa/Nairobi).
--   * status: pending -> sent, or pending -> failed once attempts run out.
--     'failed' rows stay for the System page; nothing deletes them.
--
-- RLS'd like every manager-scoped table: products enqueue through their own
-- tenant transaction. The worker is cross-tenant and runs on the admin role.
CREATE TABLE IF NOT EXISTS email_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    event_key text NOT NULL,
    recipient text NOT NULL,
    template text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz NULL,
    UNIQUE (manager_id, event_key)
);

CREATE INDEX email_outbox_due_idx ON email_outbox (next_attempt_at) WHERE status = 'pending';

ALTER TABLE email_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_outbox
    USING (manager_id = current_setting('app.tenant_id')::uuid);

-- The worker claims and settles rows across tenants (BYPASSRLS); 000013's
-- default privileges give it SELECT only.
GRANT UPDATE ON email_outbox TO willcoll_platform_admin;
