-- Per-manager, per-product entitlement (system-design.txt A.5's
-- requireModule). Disabling one never deletes that product's data — this
-- row only gates routing, checked by requireModule() in every product API.
CREATE TABLE IF NOT EXISTS manager_modules (
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste')),
    status text NOT NULL CHECK (status IN ('requested', 'trialing', 'active', 'suspended')),
    requested_at timestamptz NOT NULL DEFAULT now(),
    activated_at timestamptz NULL,
    suspended_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (manager_id, product)
);

-- Add-ons (system-design.txt B.5): landlord_portal, branding,
-- custom_sender_id, and future ones, without a schema change per add-on.
CREATE TABLE IF NOT EXISTS manager_addons (
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    addon text NOT NULL,
    status text NOT NULL CHECK (status IN ('requested', 'active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (manager_id, addon)
);
