-- Every PayHero channel belongs to exactly one product and one owner
-- (system-design.txt A.6): channel_id is PayHero's own channel identifier,
-- so it's the natural primary key rather than a surrogate uuid. owner_id is
-- an opaque id (a property, an estate, a collector zone-set) meaningful only
-- inside that product's own database — never dereferenced here.
CREATE TABLE IF NOT EXISTS payment_channels (
    channel_id text PRIMARY KEY,
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste', 'platform')),
    owner_id uuid NULL,
    callback_url text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payment_channels_manager_id_idx ON payment_channels (manager_id);

-- Deduplicates every M-Pesa receipt exactly once, before any product-
-- specific matching runs (system-design.txt A.6's claim -> post -> mark
-- posted flow). No manager_id: a webhook arrives with no tenant context
-- (ADR-0004) — the receiving product resolves manager_id from channel_id
-- after the claim succeeds, inside its own database.
CREATE TABLE IF NOT EXISTS payment_inbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mpesa_receipt text UNIQUE NOT NULL,
    channel_id text NOT NULL REFERENCES payment_channels (channel_id),
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste', 'platform')),
    amount numeric(12,2) NOT NULL,
    msisdn text NOT NULL,
    raw_payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'claimed' CHECK (status IN ('claimed', 'posted')),
    claimed_at timestamptz NOT NULL DEFAULT now(),
    posted_at timestamptz NULL
);

-- The 5-minute sweeper (system-design.txt A.6) scans exactly this.
CREATE INDEX payment_inbox_claimed_pending_idx ON payment_inbox (claimed_at) WHERE status = 'claimed';
