-- Phase 1: organizations, users, tokens, sessions, and the day-one RLS wall.
-- organizations is deliberately NOT RLS-protected: it has no organization_id
-- column because it IS the tenancy boundary (spec §1a). Every table below that
-- carries organization_id has RLS enabled with SELECT/INSERT/UPDATE/DELETE
-- policies.

CREATE TYPE subscription_tier AS ENUM ('starter', 'growth', 'professional', 'enterprise');
CREATE TYPE org_billing_status AS ENUM ('active', 'past_due', 'suspended');
CREATE TYPE user_role AS ENUM ('landlord', 'manager', 'agent');
CREATE TYPE user_status AS ENUM ('invited', 'active', 'suspended');

CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    -- NOT NULL; when a caller omits it, the tenancy service defaults it to
    -- name at insert time (Postgres cannot default one column to another).
    brand_name TEXT NOT NULL,
    subscription_tier subscription_tier NOT NULL DEFAULT 'starter',
    billing_status org_billing_status NOT NULL DEFAULT 'active',
    owner_user_id UUID,             -- nullable FK (added below) until the first user exists
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    full_name TEXT NOT NULL,
    phone TEXT NOT NULL UNIQUE,     -- Kenyan MSISDN (validated at the handler, spec §6.2)
    email TEXT NOT NULL UNIQUE,     -- activation/reset email target
    password_hash TEXT,             -- bcrypt; NULL until activated (Agent) or set (Manager)
    role user_role NOT NULL,        -- 'landlord' | 'manager' | 'agent'
    is_super_manager BOOLEAN NOT NULL DEFAULT false,
    activated BOOLEAN NOT NULL DEFAULT false,
    status user_status NOT NULL DEFAULT 'invited',
    invited_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The first Manager is created together with its Organization in one
-- transaction; owner_user_id stays nullable here and becomes NOT NULL in a
-- later migration once every legacy Organization is guaranteed to have one.
ALTER TABLE organizations
    ADD CONSTRAINT organizations_owner_user_id_fkey
    FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE SET NULL;

CREATE TABLE tokens (
    hash BYTEA PRIMARY KEY,         -- SHA-256 of the plaintext token; plaintext is never persisted
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, -- copied from user_id at insert
    expiry TIMESTAMPTZ NOT NULL,
    scope TEXT NOT NULL             -- 'activation' | 'password-reset'
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, -- copied from user_id at insert
    refresh_token_hash TEXT,
    device_label TEXT,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

-- Row-Level Security on every organization_id-bearing table (spec §1a).
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
CREATE POLICY users_select ON users FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY users_insert ON users FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY users_update ON users FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY users_delete ON users FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

ALTER TABLE tokens ENABLE ROW LEVEL SECURITY;
CREATE POLICY tokens_select ON tokens FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tokens_insert ON tokens FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tokens_update ON tokens FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tokens_delete ON tokens FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY sessions_select ON sessions FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY sessions_insert ON sessions FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY sessions_update ON sessions FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY sessions_delete ON sessions FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);
