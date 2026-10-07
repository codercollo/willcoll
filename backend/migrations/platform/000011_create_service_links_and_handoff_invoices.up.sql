-- A collector's (Waste) link to an estate or a rentals property it also
-- services (system-design.txt E.2). target_owner_id is an opaque id inside
-- the target product's own database, like payment_channels.owner_id — this
-- table never reads another product's tables, only stores the pointer.
CREATE TABLE IF NOT EXISTS service_links (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    collector_manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    target_product text NOT NULL CHECK (target_product IN ('rentals', 'estates')),
    target_owner_id uuid NOT NULL,
    consent_status text NOT NULL DEFAULT 'pending' CHECK (consent_status IN ('pending', 'granted', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_links_collector_manager_id_idx ON service_links (collector_manager_id);

-- A bulk Waste invoice raised against a linked estate/property
-- (system-design.txt E.3). Waste marks it 'paid' only when money reaches
-- its own channel through payment_inbox — until then a paying-side
-- approval shows as 'paid_unconfirmed', never 'paid'.
CREATE TABLE IF NOT EXISTS handoff_invoices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    link_id uuid NOT NULL REFERENCES service_links ON DELETE CASCADE,
    amount numeric(12,2) NOT NULL,
    period date NOT NULL,
    status text NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'approved', 'paid_unconfirmed', 'paid', 'rejected')),
    waste_invoice_ref text NULL,
    target_expense_ref text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX handoff_invoices_link_id_idx ON handoff_invoices (link_id);
