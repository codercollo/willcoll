# Willcoll — Property & Rent Management System
**Specification v0.1 — Digitizing the Willcoll Agencies rent-collection workflow**

Companion inputs used to derive this spec: the physical rent-collection receipt books (Jane Wairimu Mwangi, Kiwi Place), the hand-ruled water-meter ledgers (per-house current/previous readings → units → amount, per building per month), the printed monthly rent-roll sheet ("SABINA / NGONG"), the Neema House tenancy agreement, the water & garbage bill slip, `design-tokens.txt` (the base design system, plus the Organization white-label overlay in its §7 / spec §1b), and a reference SaaS dashboard screenshot (sidebar nav + data-table content pattern) used only to sanity-check the sidebar/header/table shell in §9, not for its color palette or copy. Architectural conventions are deliberately borrowed from two references, applied to real rent money instead of retail sales:

- **TechSchool's *Backend Master Class* (simplebank)** → the ledger: atomic, concurrency-safe, immutable double-entry money movement.
- **Alex Edwards' *Let's Go Further*** → the API shell: `httprouter`, hand-rolled middleware, structured logging, background email/SMS dispatch, graceful shutdown, permission-based auth, deployed behind Caddy on a VPS.

**Code paradigm: object-oriented, applied consistently on top of both references.** Every package named in this spec that owns behavior (`internal/money`, `internal/billing`, `internal/payments`, `internal/auth`, `internal/tenancy`, `internal/branding`, `internal/notify`, `internal/mailer`) is implemented as a `Service` struct built by a `NewService(...)` constructor, holding its own dependencies as unexported fields; its exported methods are the package's public API. `internal/domain` entities (`Lease`, `Invoice`, `MeterReading`, etc.) carry the methods intrinsic to their own fields; anything needing another package's state is a service method instead. Interfaces exist only where a genuine second implementation or test seam justifies one. HTTP handlers are thin controllers that call one service method and translate the result to JSON — never a place where business logic itself lives. This spec describes *what* the system does; `README.md` and `project-structure.txt` describe this object model in more implementation-level detail, and `prompt-phase-subphase.txt` enforces it phase by phase.

---

## 1. What the paper system is actually doing

Reading the ledgers tells you the data model before any schema is written:

| Paper artifact | What it really is |
|---|---|
| Rent collection receipt book (Jane Wairimu / Kiwi Place) | An immutable, numbered **payment entry**: house no., payer, amount, month(s) covered, method (cash/M-Pesa/cheque), a running "being payment of ... rent / water bill / arrears" narrative |
| Monthly water-meter ledger (per building, per month, per house) | A **meter reading snapshot**: current reading, previous reading, units consumed = current − previous, amount = units × rate, with a red-pen "✓" reconciliation mark once collected |
| Monthly rent-roll sheet (SABINA / NGONG, "RENT: DECEMBER 2025") | A **statement per unit per month**: date rent paid, arrears recovered, current rent due vs. paid, free-text notes ("DEC 2025 rent cleared", "part JAN 2026 rent paid") |
| Neema House Tenancy Agreement | The **lease**: landlord, tenant, house no., rent amount, due day (5th), deposit (Kes 10,000), late fee (1%/day after the 10th), utilities clause, notice period |
| Water & Garbage Collection Bill slip | A **utility invoice template**: fixed garbage fee (300), rate/m³ (200), previous balance carried forward |
| Willcoll Agencies logo / design-tokens.txt | The base design system for the **Manager/Owner back-office UI**, plus, per §1a's discovery that this is really a multi-tenant platform, the pattern for how a *different* letting business's own logo and name replace Willcoll's wherever this same UI is white-labeled (§1b) |

Three structural facts fall out of this and drive every decision below:

1. **The unit of everything is the house/unit inside a named property**, not the tenant. Tenants come and go; the house's meter, rent amount, and arrears history persist.
2. **A payment is never edited, only appended to.** The receipt book is carbon-copied and sequentially numbered — nobody tears out and rewrites entry #090. The ledger must be equally immutable.
3. **Rent, water/garbage, and electricity are three different money flows with three different collection models** — rent and garbage are landlord-billed and manager-collected; electricity is prepaid/token-based and tenant self-service, never touching the ledger except as an informational log.

---

## 1a. Multi-tenancy architecture — the Organization boundary

Willcoll is built from the ground up to host **many independent letting businesses on one shared deployment**, not just one Manager's portfolio. This is a SaaS/software-architecture sense of "tenancy" — completely distinct from a rental **Tenant** (the renter, §1, §2). To keep the two meanings from colliding on the page, this spec reserves the word **Organization** exclusively for the SaaS isolation boundary; **Tenant** always means the renter, everywhere else in this document.

**What an Organization is.** One Organization is one letting business's entire world inside Willcoll: its Managers, its Agents, its Landlords, its properties, units, leases, invoices, and ledger. A letting agency running several regional teams can have more than one Manager under a single Organization (billed as one customer, §12); a solo Manager running their own few buildings is simply an Organization of one. Either way, **no query, report, SMS, webhook, or export ever crosses an Organization boundary** — a bug in one customer's property list must be structurally incapable of leaking into another's, not merely prevented by an application developer remembering a `WHERE` clause.

**Isolation strategy: shared database, `organization_id` column, Postgres Row-Level Security.** Three standard multi-tenant strategies exist — database-per-tenant, schema-per-tenant, and shared-schema-with-a-tenant-column. Willcoll deliberately picks the third, enforced by **PostgreSQL Row-Level Security (RLS)**, not just application-level filtering:

| Strategy | Why not chosen |
|---|---|
| Database-per-Organization | Operationally expensive on a single Contabo VPS (§7) — one connection pool, one set of migrations, one backup job per tenant does not scale past a handful of customers on modest hardware |
| Schema-per-Organization | Better than DB-per-tenant, but `golang-migrate` and `sqlc` (§3, §4.3) both assume one schema; N schemas means N migration runs and N times the operational surface for the same bug class RLS solves for free |
| **Shared schema + `organization_id` + RLS (chosen)** | One Postgres instance, one migration path, one `pg_backup` job (§7) — and isolation is enforced **inside the database itself**, so even a handler that forgets to filter by Organization gets zero rows back, not another customer's ledger |

**How it's enforced, mechanically:**
1. Every top-level table (`organizations` itself excepted) carries an `organization_id UUID NOT NULL` column — either directly, or in the handful of pure join/child tables where it's inherited transitively through a `NOT NULL` FK to a row that already carries it (documented per-table in §3).
2. Every such table has `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` plus a policy of the shape `USING (organization_id = current_setting('app.current_org_id')::uuid)` — read **and** write are both gated; an `INSERT` for the wrong `organization_id` is rejected by Postgres, not just hidden from a `SELECT`.
3. `internal/tenancy`'s request-scoped middleware resolves `organization_id` from the caller's PASETO token (a claim set at login/invite time, alongside role) and runs `SET LOCAL app.current_org_id = '<uuid>'` as the first statement of every request's DB transaction — so **every** query that transaction runs, no matter which handler or which package wrote it, is automatically scoped, with no per-query `WHERE organization_id = ?` to remember or forget.
4. A separate, internal-only Postgres role (`platform_admin`, granted `BYPASSRLS`) exists solely for Willcoll's own cross-tenant support/billing tooling (e.g. computing every Organization's unit count for §12 invoicing) — it is never reachable from the public API, and every use of it is logged.

**Relationship to RBAC/PBAC (§2).** Multi-tenancy and RBAC/PBAC solve different problems and both apply, layered: RLS/`organization_id` answers *"whose data can this request ever touch, full stop"*; RBAC (role) and PBAC (`agent_property_grants`) answer *"within that Organization's data, what is this specific user allowed to do."* An Agent still can't record a payment on a property they haven't been granted, even though RLS has already correctly let them see other data inside the same Organization.

**What this changes elsewhere in this spec:** §3 adds the `organizations` table and an `organization_id` column across the schema; §6 adds the tenant-resolution middleware to the request pipeline; §7 notes the backup/restore implication of a shared database; §12 bills at the Organization level (not strictly "per Manager," since one Organization can hold several Managers).

---

## 1b. Dynamic Organization branding — white-labeling the same deployment

Because Willcoll is multi-tenant (§1a), the word "Willcoll" appearing anywhere in the *product itself* — the dashboard header, the browser tab title, an SMS receipt, an Agent's activation email — is a bug, not a feature, for every Organization that isn't literally the Willcoll Agencies Organization. A Manager running their letting business as "Rentman" must never see or send anything branded "Willcoll." This section specifies the mechanism that makes that true everywhere, automatically, rather than relying on every future template author to remember it.

**What's brandable, and what deliberately isn't.** Two failure modes exist and this spec picks a fixed middle point between them: (a) hardcoding "Willcoll" everywhere, which makes white-labeling impossible, and (b) letting an Organization override arbitrary CSS, which breaks the design system's typography, spacing, motion, and accessibility guarantees documented in `design-tokens.txt` and produces an inconsistent, harder-to-support product per customer. Willcoll allows exactly five things per Organization, and locks everything else:

| Brandable | Locked (never Organization-editable) |
|---|---|
| `brand_name` — the business name shown in the UI, SMS, and email | Typography (Fraunces/General Sans and the full type scale) |
| `logo_url` — uploaded logo, shown in the sidebar/header and email header | Spacing, grid, radius, motion timings/easing |
| `accent_key` — ONE accent color, chosen from a fixed, pre-approved palette (§1b.3), never a freeform hex | The neutral/surface palette, status colors (success/warning/error) |
| `sms_sender_id` — the alphanumeric sender name tenants see on an SMS (subject to the SMS gateway's own registration rules) | Layout structure: sidebar width, card grid, table/ledger layouts |
| `email_from_name` — the display name on outbound Agent/Manager emails | Iconography, elevation/shadow treatment, all UI copy other than the brand name itself |

This mirrors the same philosophy as PBAC narrowing a role's *default* bundle (§2.2) without ever letting an Agent grant themselves a capability outside that bundle: an Organization can restyle a small, fixed surface area, never the system underneath it.

### 1b.1 Schema addition (extends `organizations`, §3.1)

```sql
-- ALTER TABLE organizations ADD COLUMN ...
brand_name        TEXT NOT NULL,             -- defaults to `organizations.name` at creation time; editable
                                              -- separately afterward (a business's LEGAL name and its
                                              -- DISPLAY/brand name are allowed to diverge, e.g. legal name
                                              -- "Willcoll Agencies Ltd", brand_name "Willcoll")
slug              TEXT NOT NULL UNIQUE,       -- URL-safe (lowercase, hyphens only), generated from brand_name
                                              -- at signup and editable by the Manager afterward as long as
                                              -- the new value is still unique; used in the frontend URL
                                              -- prefix (/{slug}/login) and the public branding lookup (§1b.2)
logo_url          TEXT,                       -- NULL until a logo is uploaded; falls back to a generic
                                              -- "letting business" placeholder mark in the UI, never to
                                              -- another Organization's logo and never to a hardcoded
                                              -- Willcoll logo
accent_key        TEXT NOT NULL DEFAULT 'orange',  -- one of the fixed keys in §1b.3 — a CHECK constraint,
                                              -- not a free string, so an invalid value can never reach this
                                              -- column even if application-level validation is bypassed
sms_sender_id     TEXT,                       -- NULL falls back to a platform-default sender ID; Managers
                                              -- who want their own must register it with the SMS gateway
                                              -- first (spec §11.3) — Willcoll stores it, doesn't grant it
email_from_name   TEXT,                       -- NULL falls back to "{brand_name} via Willcoll" so recipients
                                              -- still recognize the platform in shared-domain deliverability
                                              -- edge cases; set explicitly once an Organization is established
```

`brand_name`, `slug`, `logo_url`, and `accent_key` are not tenancy columns — they carry no isolation meaning and are readable by the *unauthenticated* public branding endpoint below by design (a login screen needs to know what to paint before anyone has logged in). RLS (§1a) still governs everything else on `organizations` and every other table; this is the one narrow, intentional exception, and it exposes only these five columns, never anything else on the row.

### 1b.2 How branding reaches the frontend and outbound messages

- **Frontend, pre-login.** The Nuxt static export is one build serving every Organization, so it cannot bake branding in at build time. Instead, `GET /v1/public/branding?slug={slug}` (unauthenticated, no `app.current_org_id` needed since it takes an explicit slug and reads only the five public columns above) is called by `plugins/branding.client.ts` before the app mounts, resolving `{slug}` from the URL path prefix (e.g. `example.com/rentman/login`, per the shared-domain strategy in §1b.4). The result populates `useBranding()` and sets the document title, favicon, and the CSS custom properties `--brand-name`, `--brand-logo-url`, `--color-action-primary` (resolved from `accent_key`, §1b.3) — everything else in `tokens.css` stays exactly as `design-tokens.txt` defines it.
- **Frontend, post-login.** The authenticated session already carries `organization_id` (§1a); `GET /v1/organization` (§6.1) returns the same five branding fields alongside `subscription_tier`/`billing_status`, so a Manager never has to re-enter their own slug once logged in.
- **SMS and email.** `internal/branding.BrandVars(organizationID)` returns `{BrandName, LogoURL}` and is called by `internal/notify` and `internal/mailer` immediately before rendering any template (§ project-structure.txt `template_vars.go`). Every `.tmpl` file references `{{.BrandName}}` — never a literal "Willcoll" — so a template change is never required to onboard a new Organization's brand.

### 1b.3 The approved accent palette

`accent_key` is a closed enum, not a color picker, because an arbitrary hex can silently fail the contrast ratios `design-tokens.txt` was built around (buttons, focus rings, status chips). Five pre-vetted accents ship in v0.1, each already checked for WCAG AA contrast against both the light and dark surface tokens: `orange` (the Willcoll Agencies default), `teal`, `indigo`, `crimson`, `forest`. Adding a sixth is a design-system change (a new row in this table plus a contrast check), not something an Organization can trigger by typing a hex code into a form field.

### 1b.4 What's explicitly out of scope for v0.1

- **Custom domains per Organization** (e.g. `app.rentman.co.ke` instead of a shared-domain `/rentman/` slug prefix) — tracked as an open question, §11.8. v0.1 ships one shared domain with a URL slug per Organization; Caddy's config does not need per-tenant TLS provisioning yet.
- **Per-property branding beneath the Organization level** — §9.2's existing per-property logo upload (falling back to the *Organization's* brand mark, not a hardcoded Willcoll mark) is unaffected and stays as-is; it sits one level below Organization branding, not in tension with it.
- **Fully custom typography/layout per Organization** — deliberately locked, per the table above, to keep every Organization's instance auditable against the same design system and equally accessible.

---

## 2. Actors, roles & the permission model (RBAC + PBAC)

Four actor types, three of which are system users — every one of them belongs to exactly one Organization (§1a); nothing below overrides that outer boundary, it only governs what's allowed *inside* it:

| Actor | System user? | Primary channel | Core idea |
|---|---|---|---|
| **Landlord (Owner)** | Yes — read-mostly | Web dashboard + **SMS alerts** | Owns one or more properties within one Organization; wants visibility and peace of mind, not data entry |
| **Manager** | Yes — full operator | Web/desktop dashboard | Runs day-to-day operations across the properties assigned to them, within their Organization; the system's main actor |
| **Agent** | Yes — scoped operator | Web dashboard | Invited **by a Manager** to help run specific properties in that same Organization, under a permission set the Manager controls |
| **Tenant** | No login — SMS only | **SMS** | Receives rent due/received/arrears/receipt SMS; never touches the UI. (This is the renter — see §1a for why this word never refers to the SaaS isolation boundary.) |

### 2.1 RBAC: role → base capability set

RBAC gives each account a **role**, which grants a *default* bundle of permissions. PBAC (below) then lets a Manager narrow an Agent's bundle per property.

| Capability | Landlord | Manager | Agent (default, pre-narrowing) | Tenant |
|---|:---:|:---:|:---:|:---:|
| View own property portfolio (read-only) | ✅ | ✅ | ✅ (assigned only) | — |
| Record rent/water/garbage payment | — | ✅ | ✅ | — |
| Record meter reading | — | ✅ | ✅ | — |
| Issue invoices / arrears statements | — | ✅ | ✅ | — |
| Edit unit rent amount, deposit, rate card | — | ✅ | ✅ if granted | — |
| Create/close a lease (move-in / move-out) | — | ✅ | ✅ if granted | — |
| Add/remove a property (apartment) | ✅ (own) | ✅ | — | — |
| **Invite an Agent** | — | ✅ | — | — |
| Set Agent's permission scope | — | ✅ | — | — |
| View the immutable ledger (all entries) | ✅ (own properties) | ✅ | ✅ (view-only unless granted) | — |
| Reverse/void a payment (never edit — always a reversing entry) | — | ✅ | ✅ if granted | — |
| View consolidated financial reports across properties | ✅ (own) | ✅ | — | — |
| Configure SMS templates / alert thresholds | ✅ (own prefs) | ✅ | — | — |
| Receive rent-due / rent-received / arrears SMS | ✅ (summary) | — | — | ✅ |
| Self-report a utility token top-up (informational) | — | — | — | ✅ (SMS/USSD-style flow) |
| System/backup/audit-log administration | — | ✅ (super-manager flag) | — | — |

### 2.2 PBAC: per-property, per-Agent narrowing

An Agent's row in `agent_property_grants` carries a **permission bitset scoped to one property**, not the whole system:

```
grant = {
  agent_id, property_id,
  can_record_payments      bool,
  can_edit_leases          bool,
  can_edit_unit_pricing    bool,
  can_void_payments        bool,
  can_view_financial_reports bool,
  can_manage_meter_readings bool,
  granted_by (manager_id), granted_at, revoked_at
}
```

This means: **Manager A can invite the same human as an Agent on Property 1 with full rights, and Property 2 with view-only rights** — matching how Willcoll actually delegates a caretaker to one building but not another.

### 2.3 Landlord scoping

A Landlord is attached to one or more properties via `property_ownership(property_id, landlord_id, ownership_pct)`. All Landlord-facing queries and SMS are filtered through this table — a Landlord can never see another landlord's building even if the same Manager runs both (mirrors the RipplePOS Agent trust-boundary pattern: the read boundary is enforced in the query layer, not a UI toggle). This is the **within-Organization** boundary; the Organization-vs-Organization boundary underneath it is RLS (§1a), so a Landlord can't see another Organization's building either, for a much simpler reason — those rows are never returned to their session at all.

### 2.4 Manager payout onboarding (KYC) — a prerequisite role state

Before a Manager can receive **any** tenant-initiated digital payment (M-Pesa STK push or card, §5.5), they must complete a one-time payout onboarding step, separate from the account-activation flow in §3.1a:

| Field | Purpose |
|---|---|
| Legal payout bank details | Bank name, bank branch code, account name, account number — where the Manager's settlements/payouts clear |
| KYC / business registration docs | KRA PIN certificate, and either a Certificate of Incorporation (registered company) or National ID (sole proprietor) — whatever the payment gateway requires to clear the Manager for settlement |
| Gateway sub-account ID | Opaque ID returned by the gateway once KYC clears — stored against the Manager (and scoped to their Organization, §1a), never generated locally |

Until this is complete, a Manager's properties can still operate fully on manual collection (cash/M-Pesa-manual/bank/cheque, recorded by the Manager exactly as today) — digital tenant self-pay is additive, not a hard dependency. See §5.5 for the full flow and §3.1b for the schema.

---

## 3. Database schema (PostgreSQL, sqlc, golang-migrate)

### 3.1 Identity & access

```sql
organizations (                       -- the multi-tenancy boundary itself (§1a) — one row per letting business
  id UUID PK,
  name TEXT NOT NULL,                 -- the business/brand name, e.g. "Willcoll Agencies"
  subscription_tier subscription_tier NOT NULL DEFAULT 'starter',  -- starter|growth|professional|enterprise
  billing_status org_billing_status NOT NULL DEFAULT 'active',     -- active|past_due|suspended
  owner_user_id FK users,             -- the Manager who signed up / is billed; nullable until that user exists
                                       -- (deferred FK — the first Manager row references this org's id first)
  created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ
)
-- Every other table in this schema either carries organization_id directly, or inherits it transitively
-- through a NOT NULL FK to a row that does (noted inline per table below). Row Level Security is enabled
-- on every organization_id-bearing table with a USING (organization_id = current_setting('app.current_org_id')
-- ::uuid) policy — see §1a for the full mechanism and §6.2 for where the session variable gets set.

users (
  id UUID PK,
  organization_id FK organizations NOT NULL,   -- <-- the tenancy column; RLS-enforced (§1a)
  full_name TEXT NOT NULL,
  phone TEXT UNIQUE NOT NULL,          -- Kenyan MSISDN, e.g. 2547XXXXXXXX
  email TEXT UNIQUE NOT NULL,           -- required for manager/agent — the activation/reset email target
  password_hash TEXT,                   -- bcrypt; NULL until the account is activated (Agent) or set (Manager)
  role user_role NOT NULL,              -- 'landlord' | 'manager' | 'agent'
  is_super_manager BOOL DEFAULT false,  -- an Organization-scoped admin flag (§2.1) — NOT the cross-Organization
                                         -- platform_admin Postgres role from §1a, which no application user maps to
  activated BOOL NOT NULL DEFAULT false,   -- Let's Go Further ch.13/15 pattern
  status user_status NOT NULL DEFAULT 'invited',  -- invited|active|suspended
  invited_by FK users,                  -- the Manager who created this Agent (NULL for self-serve Landlord signup);
                                         -- always within the same organization_id, enforced at the handler level
  created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ
)
-- Manager accounts: the FIRST Manager on a new Organization is created together with that organizations row
-- in one transaction (self-signup creates both); created with activated=true, status='active', password_hash
-- set immediately (Manager sets their own password on sign-up) — no activation email is ever sent. A second
-- Manager on an existing Organization (e.g. a regional hire, §1a) is invited by an existing Manager the same
-- way an Agent is (§3.1a), just with role='manager' instead of 'agent'.
-- Agent accounts: created with activated=false, status='invited', password_hash=NULL, inheriting the inviting
-- Manager's organization_id — see §3.1a.

tokens (                              -- Let's Go Further ch.15.1 pattern, verbatim: a single generic table
  hash BYTEA PRIMARY KEY,              -- SHA-256 hash of the plaintext token; plaintext exists ONLY in the
                                        -- emailed link and is never persisted or logged
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,   -- organization_id inherited via user_id
  expiry TIMESTAMPTZ NOT NULL,         -- activation: 3 days; password-reset: 45 minutes
  scope TEXT NOT NULL                  -- 'activation' | 'password-reset'
)
-- Generating a new token of a given scope for a user deletes that user's prior unexpired tokens of the
-- same scope first (one live activation link and one live reset link per user at a time — mirrors ch.15.2
-- and ch.22.1's "Additional Activation Tokens" / "Managing Password Resets" appendices).

sessions (
  id UUID PK, user_id FK users, refresh_token_hash TEXT,   -- organization_id inherited via user_id
  device_label TEXT, expires_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ
)

property_ownership (               -- organization_id inherited via property_id (both FKs are same-org by
  property_id FK, landlord_id FK users, ownership_pct NUMERIC(5,2) DEFAULT 100.00,   -- construction, enforced
  PRIMARY KEY (property_id, landlord_id)                                            -- at the handler level)
)

agent_property_grants (            -- organization_id inherited via property_id
  id UUID PK, agent_id FK users, property_id FK properties,
  can_record_payments BOOL, can_edit_leases BOOL, can_edit_unit_pricing BOOL,
  can_void_payments BOOL, can_view_financial_reports BOOL, can_manage_meter_readings BOOL,
  granted_by FK users, granted_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ
)
```

### 3.1a Manager vs. Agent lifecycle — activation & password reset (Alex Edwards style, SMTP/Mailtrap)

Managers and Agents are provisioned differently on purpose, matching how the business actually delegates access:

**Manager — activated by default.** A Manager signs up (or is provisioned as a super-manager) and sets their own password at creation time in the same request: `users` row is inserted with `activated = true`, `status = 'active'`, `password_hash` set immediately. No token, no email — a Manager is trusted the moment their account exists and can log in right away.

**Agent — invited, not trusted until they activate.** A Manager can never set an Agent's password. Instead:

1. **Invite.** `POST /v1/agents` (Manager only) creates the `users` row with `activated = false`, `status = 'invited'`, `password_hash = NULL`, `invited_by = <manager_id>`, `organization_id = <inviting manager's organization_id>` (an Agent can never be created into a different Organization than the Manager inviting them — enforced by the handler reading the caller's own `organization_id` off their session, never accepting one from the request body). The handler generates a cryptographically random 26-byte token (ch.15.2 pattern: `rand.Read` → base32 encode for the emailed link, SHA-256 hash stored in `tokens` with `scope = 'activation'`, `expiry = now()+3 days`).
2. **Email, not SMS.** The plaintext token is embedded in an activation link and sent via **SMTP** using `internal/mailer` (background-dispatched, Let's Go Further ch.14 pattern) — **Mailtrap** in development/staging, a real transactional SMTP provider in production, both behind the same `internal/mailer.Mailer` interface so swapping providers is a config change, not a code change. Template: **`activation-password.tmpl`**.
3. **Agent clicks the link** → lands on the Nuxt `web/app` page `pages/activate.vue?token=...` → that page calls `PUT /v1/users/activate {token, password}` — the Agent sets their password **for the first time** in this same call. The handler looks up `tokens` by `SHA-256(token)`, checks `scope='activation'` and `expiry > now()`, sets `users.activated = true`, `status = 'active'`, `password_hash = bcrypt(password)`, then deletes that token row (single-use, ch.15.4).
4. **Redirect to `/login`.** On success the frontend redirects the Agent straight to the login page — they log in with the email + password they just set.

**Agent password reset — Manager-initiated, not self-service.** An Agent never triggers their own reset from a "forgot password" link; the Manager does it on the Agent's behalf, from the Agents & Permissions screen (§9.5):

1. Manager clicks **"Reset password"** next to an Agent → `POST /v1/agents/:id/reset-password` (Manager only; permission-gated the same way as any other Manager-only action). This generates a new token exactly as above but with `scope = 'password-reset'`, `expiry = now()+45 minutes` (short-lived — ch.22.1's "Managing Password Resets" convention), and **invalidates any prior unexpired reset token for that Agent first**.
2. `internal/mailer` sends the Agent a reset link using template **`reset-password.tmpl`** — again SMTP/Mailtrap, background-dispatched.
3. Agent clicks the link → `pages/reset-password.vue?token=...` → `PUT /v1/users/password {token, new_password}` → handler validates the `password-reset`-scoped token, updates `password_hash`, deletes the token, and **revokes all of that Agent's existing `sessions`** (forces re-login everywhere, standard reset hygiene).
4. **Redirect to `/login`.** Agent logs in fresh with the new password.

The Manager is never shown or asked to choose the Agent's new password at any point — they only trigger the email; the Agent is the only party who ever types the actual password, matching Alex Edwards' separation between "issue a token" and "consume a token" as two independently-secured steps.

**PASETO tokens carry `organization_id`.** Every issued access token (Manager, Agent, or Landlord) embeds `organization_id` as a claim alongside `role` and `user_id` (§6.2, §1a). `internal/tenancy`'s middleware reads this claim — never the request body or path — to `SET LOCAL app.current_org_id` for RLS; a client cannot request "show me a different Organization's data" by editing a query parameter, because the org boundary is resolved server-side from a token nobody but Willcoll can forge (PASETO `v2.local` is symmetrically encrypted, §"Tech stack").

### 3.1b Payment gateway integration — IntaSend sub-accounts & webhook ledger (schema)

Willcoll uses **[IntaSend](https://intasend.com/)** as its payment gateway: it is built for the Kenyan market, handles M-Pesa STK-push collection and card processing natively, and can settle directly to a Manager's own bank account or mobile wallet — a strong local alternative to a generic global processor. Willcoll follows the **Merchant Sub-Account model**: one master IntaSend developer/business account belongs to Willcoll, and each Manager is provisioned as a **sub-account** under it, so tenant payments route and settle straight to the correct Manager without Willcoll ever holding client funds itself.

```sql
manager_payout_accounts (
  id UUID PK, organization_id FK organizations NOT NULL, manager_id FK users NOT NULL UNIQUE,
  bank_name TEXT NOT NULL, bank_branch_code TEXT, account_name TEXT NOT NULL, account_number TEXT NOT NULL,
  kra_pin TEXT NOT NULL, business_doc_type payout_doc_kind NOT NULL,  -- 'certificate_of_incorporation' | 'national_id'
  business_doc_reference TEXT NOT NULL,      -- pointer to the stored KYC document, not the raw file
  gateway_provider TEXT NOT NULL DEFAULT 'intasend',
  gateway_subaccount_id TEXT UNIQUE,         -- e.g. "ACCT_xxxxxx" — set only once IntaSend clears KYC
  kyc_status payout_kyc_status NOT NULL DEFAULT 'pending',  -- pending | verified | rejected
  kyc_submitted_at TIMESTAMPTZ, kyc_verified_at TIMESTAMPTZ, kyc_rejection_reason TEXT,
  created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ
)
-- One row per Manager (a Manager, not the Organization, is the settlement identity IntaSend KYCs — an
-- Organization with several Managers, §1a, can therefore have several payout accounts, one per Manager,
-- each still filtered by the same organization_id via RLS). can_accept_digital_payments (a derived flag,
-- not stored) = kyc_status = 'verified' AND gateway_subaccount_id IS NOT NULL. Properties/units run fine
-- without this row — see §2.4.

payment_gateway_transactions (        -- the webhook inbox — this table is what makes double-delivery harmless
  id UUID PK, organization_id FK organizations NOT NULL,   -- resolved from the invoice/unit at initiation time
  gateway_provider TEXT NOT NULL DEFAULT 'intasend',
  gateway_reference TEXT NOT NULL,         -- IntaSend's checkout/invoice/transaction ID
  gateway_subaccount_id TEXT NOT NULL,     -- which Manager this settled to
  unit_id FK units, invoice_id FK invoices,  -- resolved from the checkout metadata set at initiation
  amount NUMERIC(12,2) NOT NULL, currency TEXT NOT NULL DEFAULT 'KES',
  channel TEXT NOT NULL,                   -- 'mpesa' | 'card'
  status TEXT NOT NULL,                    -- 'PENDING' | 'COMPLETE' | 'FAILED' as reported by IntaSend
  raw_payload JSONB NOT NULL,              -- full webhook body, kept for dispute/audit purposes
  ledger_transfer_id FK ledger_transfers,  -- set once successfully posted — NULL until then
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (gateway_provider, gateway_reference)   -- <-- the actual duplicate-prevention mechanism
)
-- The IntaSend webhook handler is Willcoll's one deliberate, audited exception to "middleware always sets
-- app.current_org_id from a token" (§1a) — IntaSend is not a logged-in Organization user, so the handler
-- resolves organization_id itself from the gateway_reference's checkout metadata before writing this row,
-- under the platform_admin role, then every downstream write (the ledger transfer) proceeds normally scoped.
```

**Why duplicates can't happen even though webhooks aren't exactly-once:** IntaSend (like any gateway) can retry a webhook delivery, and a tenant can also double-tap "Pay Now" and trigger two checkout sessions for the same invoice. Two independent guards close this off:
1. `payment_gateway_transactions.gateway_reference` is the gateway's own unique ID for that specific payment attempt, `UNIQUE`-constrained — a re-delivered webhook for the same reference is caught at the DB level before it ever reaches `internal/money`.
2. Every successful transaction still flows through `ExecuteRentPaymentTx`/`ExecuteWaterGarbageTx` with its own derived `idempotency_key` (§4.3) on `ledger_transfers`, so even a theoretical second *distinct* gateway reference for the same invoice/period/amount is caught by the ledger's own idempotency guard, not just the webhook table's.

### 3.2 Properties, units, tenants, leases

```sql
properties (                      -- "SABINA", "NGONG", "Kiwi Place", "Neema House"
  id UUID PK,
  organization_id FK organizations NOT NULL,   -- <-- the tenancy column (§1a); RLS-enforced
  manager_id FK users NOT NULL,   -- the Manager who administers it day-to-day — always same-org as above,
                                   -- enforced by the handler resolving both from the same authenticated session
  name TEXT NOT NULL,
  location TEXT NOT NULL,         -- free text + optional lat/lng for the map card
  brand_note TEXT,                -- e.g. "P.O. Box 829, Naivasha or 63053, Nairobi"
  water_rate_per_m3 NUMERIC(10,2) DEFAULT 200.00,
  garbage_fee_flat NUMERIC(10,2) DEFAULT 300.00,
  late_fee_pct_per_day NUMERIC(5,2) DEFAULT 1.00,
  rent_due_day SMALLINT DEFAULT 5,
  created_at TIMESTAMPTZ
)

units (                           -- "HSE NO." rows in the ledgers — the durable entity
  id UUID PK, property_id FK properties NOT NULL,    -- organization_id inherited via property_id
  unit_label TEXT NOT NULL,       -- "A1","B5","Shop 3","House No. 1"
  unit_type TEXT,                 -- residential | shop
  base_rent NUMERIC(10,2) NOT NULL,
  deposit_amount NUMERIC(10,2) NOT NULL,
  status unit_status DEFAULT 'vacant',   -- vacant | occupied | notice_given
  UNIQUE (property_id, unit_label)
)

tenants (                          -- the renter — organization_id carried directly since a tenant can, in
  id UUID PK, organization_id FK organizations NOT NULL,  -- principle, exist before any lease row references
  full_name TEXT NOT NULL, phone TEXT NOT NULL,   -- SMS target        -- them (matches an Agent pre-registering
  id_number TEXT, created_at TIMESTAMPTZ                              -- a tenant ahead of move-in day)
)

leases (                          -- one row per Neema-House-style agreement
  id UUID PK, unit_id FK units NOT NULL, tenant_id FK tenants NOT NULL,  -- organization_id inherited via unit_id
  monthly_rent NUMERIC(10,2) NOT NULL,     -- snapshot at signing; unit.base_rent may drift later
  deposit_paid NUMERIC(10,2) NOT NULL,
  start_date DATE NOT NULL, end_date DATE,  -- NULL while active
  rent_due_day SMALLINT NOT NULL,
  late_fee_pct_per_day NUMERIC(5,2) NOT NULL,
  status lease_status DEFAULT 'active',     -- active | terminated | notice_period
  terminated_reason TEXT, terminated_at TIMESTAMPTZ,
  created_by FK users, created_at TIMESTAMPTZ
)
-- Constraint: at most one 'active' lease per unit at a time (partial unique index).
```

### 3.2a Bulk CSV import — tenants & units, tied to a Landlord

Onboarding a new property by hand, one unit and one tenant at a time, is the same friction the paper ledger already had — a Manager or Agent moving a whole existing building onto Willcoll (e.g. 30 units of SABINA / NGONG, each with a sitting tenant) needs a single import, not thirty form submissions. This is a **second, tenant-focused CSV import**, distinct from the existing unit-ledger CSV mentioned in `project-structure.txt`/`examples/` (which mirrors only the paper meter-reading columns for an already-created unit) — this one onboards **units and their sitting tenants together**, and only ever runs **after** the property itself exists and is attached to a Landlord.

**Sequence, matching how a Manager actually works:**
1. Manager creates the `property` (`POST /v1/properties`) — name, location, rates (§3.2).
2. Manager attaches it to a Landlord via `property_ownership` (§3.1, either at creation or immediately after) — the CSV import in step 3 is rejected if the property has no `property_ownership` row yet, since a unit/tenant/lease onboarded with no Landlord of record defeats the whole point of §13's Verified Property Score later.
3. Manager or Agent (if granted `can_edit_leases`, §2.2) uploads the CSV via `POST /v1/properties/:id/tenants/import` — one row per occupied (or vacant) unit.

**CSV columns** (header row required, order-independent, matching what already exists on a paper rent-roll sheet, §1):

```
unit_label, unit_type, base_rent, deposit_amount, tenant_full_name, tenant_phone, tenant_id_number,
lease_start_date, monthly_rent, rent_due_day, late_fee_pct_per_day
```

- `unit_label` is required and must be unique within the property (`UNIQUE (property_id, unit_label)`, §3.2) — a duplicate label within the same file, or one that already exists on that property, fails that row without failing the whole file.
- `tenant_full_name`/`tenant_phone`/`lease_start_date` may be blank together — a blank tenant means the row only creates a **vacant** unit (`units.status='vacant'`), no `tenants` or `leases` row; every tenant-related column must be blank together or filled together, never partially, or the row fails validation.
- `monthly_rent`, `rent_due_day`, `late_fee_pct_per_day` default to the unit's / property's own values when left blank (mirrors how a `lease` row snapshots `property` defaults at signing, §3.2) — a Manager only fills these in when a specific tenant's terms differ from the property standard.
- Kenyan MSISDN validation (`pkg/msisdn`) and KES amount validation (`pkg/kesmoney`) run per-row, identically to the equivalent JSON endpoints — the CSV path is not a validation-lite shortcut.

**Import semantics — one file, one atomic-per-row outcome, never a partial ledger write:**
- The whole file is parsed and validated **before** any database write begins; a structurally broken file (wrong headers, unparseable rows) is rejected with zero side effects.
- Each valid row is then processed as its own **unit-creation + optional-tenant-creation + optional-lease-creation** transaction (reusing `unit.go`/`tenant.go`/`lease.go` domain constructors, §"Code paradigm") — a single bad row (e.g. a duplicate unit label) is skipped and reported, it does not roll back the rows that already succeeded, matching how a Manager actually recovers from a typo on row 14 of a 30-row sheet: fix that one row and re-upload only it, not the whole building.
- A lease created by import posts the same `ExecuteDepositTx` (§4.3) as a lease created by hand through `POST /v1/units/:id/leases` — the money-side of onboarding a sitting tenant is never special-cased just because it arrived via CSV. No rent-payment history is fabricated for a sitting tenant's prior (pre-Willcoll) months; the ledger's history for that unit genuinely starts on the import date, same as any other unit — this matters directly for §13's Verified Property Score, which must never be seeded with invented data.
- The response returns a per-row result list (`created` / `skipped` + reason), never a bare success/fail boolean, so the Manager can see exactly which rows to fix.

**Schema — import audit trail:**

```sql
tenant_import_batches (           -- one row per CSV upload, for audit + idempotent re-upload safety
  id UUID PK, organization_id FK organizations NOT NULL,   -- <-- tenancy column (§1a)
  property_id FK properties NOT NULL,
  uploaded_by FK users NOT NULL,
  filename TEXT NOT NULL,
  row_count INT NOT NULL, created_count INT NOT NULL, skipped_count INT NOT NULL,
  row_results JSONB NOT NULL,      -- per-row created/skipped + reason, exactly what the response returned
  created_at TIMESTAMPTZ
)
```

### 3.3 Meters, readings, utility billing

```sql
meters (
  id UUID PK, unit_id FK units NOT NULL, meter_type meter_kind NOT NULL,  -- 'water'
  meter_number TEXT
)

meter_readings (                  -- mirrors "current/previous reading, units, amount"
  id UUID PK, meter_id FK meters NOT NULL,   -- organization_id inherited via meter_id → unit_id
  reading_month DATE NOT NULL,    -- first-of-month key, e.g. 2026-08-01
  previous_reading NUMERIC(10,2) NOT NULL,
  current_reading NUMERIC(10,2) NOT NULL,
  units_consumed NUMERIC(10,2) GENERATED ALWAYS AS (current_reading - previous_reading) STORED,
  rate_per_m3 NUMERIC(10,2) NOT NULL,
  amount_due NUMERIC(10,2) GENERATED ALWAYS AS ((current_reading - previous_reading) * rate_per_m3) STORED,
  recorded_by FK users NOT NULL, recorded_at TIMESTAMPTZ,
  UNIQUE (meter_id, reading_month)
)
```

Electricity is **not** modeled here beyond an informational log — see §5.4.

### 3.4 Invoices — what is owed, distinct from what is paid

```sql
invoices (
  id UUID PK, organization_id FK organizations NOT NULL,   -- carried directly (not just via lease_id) so
                                                             -- §4's ledger-posting queries and RLS policies
                                                             -- never need a multi-hop join through leases →
                                                             -- units → properties just to know whose row it is
  lease_id FK leases NOT NULL,
  period_month DATE NOT NULL,           -- which month this invoice covers
  invoice_type invoice_kind NOT NULL,   -- 'rent' | 'water_garbage' | 'deposit' | 'late_fee'
  amount_due NUMERIC(12,2) NOT NULL,
  amount_paid NUMERIC(12,2) NOT NULL DEFAULT 0,   -- denormalized, kept in sync ONLY by the money package (§4)
  balance NUMERIC(12,2) GENERATED ALWAYS AS (amount_due - amount_paid) STORED,
  status invoice_status NOT NULL DEFAULT 'open',  -- open | partially_paid | paid | waived
  meter_reading_id FK meter_readings,   -- set when invoice_type='water_garbage'
  created_at TIMESTAMPTZ,
  UNIQUE (lease_id, period_month, invoice_type)
)
```

`amount_paid`/`status` on `invoices` are **read-model fields** — the only writer is the ledger-posting transaction in `internal/money`, exactly as RipplePOS's `internal/money` owns `accounts.balance_kes`. No handler ever `UPDATE`s an invoice directly.

### 3.5 Verified Property Score — schema

Full behavior in §13; this is only the storage shape, placed here since it depends on `properties`/`leases`/`invoices`/the ledger (§4) already being defined.

```sql
organization_addons (              -- premium add-ons billed the SAME way as the base subscription (§12.1) —
  id UUID PK, organization_id FK organizations NOT NULL,   -- <-- tenancy column (§1a)
  addon_key addon_kind NOT NULL,   -- 'verified_property_score' (only value in v0.1, left as an enum for
                                    -- future add-ons rather than a one-off boolean column)
  status addon_status NOT NULL DEFAULT 'inactive',   -- inactive | active | past_due | cancelled
  monthly_fee_kes NUMERIC(10,2) NOT NULL,   -- flat per-Organization fee, §12.3 — not per-property, per-score
  activated_at TIMESTAMPTZ, cancelled_at TIMESTAMPTZ,
  UNIQUE (organization_id, addon_key)
)
-- Gates access exactly the way subscription_tier already gates unit count (§12.1): a handler (or the
-- FastAPI sidecar's own dependency, project-structure.txt app/dependencies.py) checks
-- organization_addons.status = 'active' for addon_key='verified_property_score' before running or
-- returning a score — never a client-side-only check.

property_scores (                  -- append-only, one new row per computation — NEVER updated (mirrors
  id UUID PK, organization_id FK organizations NOT NULL,   -- <-- tenancy column, resolved via property_id
                                                             -- at write time, carried directly for the same
                                                             -- reason invoices.organization_id is (§3.4):
                                                             -- no multi-hop join needed just to RLS-scope it
  property_id FK properties NOT NULL,
  computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  as_of_date DATE NOT NULL,          -- the ledger cutoff the score was computed against
  model_version TEXT NOT NULL,       -- matches ml-sidecar/ml/model_card.md's version, §13.1
  noi NUMERIC(14,2) NOT NULL,        -- Net Operating Income over the scoring window
  dscr NUMERIC(6,3) NOT NULL,        -- Debt Service Coverage Ratio
  score_value NUMERIC(6,2) NOT NULL, -- raw model output
  score_band TEXT NOT NULL,          -- lender-familiar bucket, e.g. 'A' | 'B' | 'C' | 'D' (§13.2)
  feature_snapshot JSONB NOT NULL,   -- the exact feature vector fed to the model — reproducibility +
                                      -- audit trail; never regenerated retroactively from a later ledger
                                      -- state, since that would defeat §13's whole premise
  requested_by FK users NOT NULL,    -- always a Manager (§13.4) — never a Landlord, who is SMS/read-only
  UNIQUE (property_id, as_of_date, model_version)
)
-- No UPDATE, no DELETE path exists anywhere in the codebase for this table (enforced at the sqlc query
-- layer, mirrors §4.2's ledger_entries) — a re-run for the same property produces a NEW row with a new
-- as_of_date, so a Landlord's score history is itself an auditable timeline, not a single mutable number.
```

---

## 4. The ledger — immutable, double-entry, simplebank-pattern

This is the part that must never be "clever." Every shilling that moves is a transfer between two accounts, written once, never edited.

### 4.1 Accounts

```sql
ledger_accounts (
  id UUID PK,
  organization_id FK organizations NOT NULL,   -- carried directly (§1a) — the ledger is the most
                                                 -- sensitive data in the system; RLS on this table alone
                                                 -- is the single most important isolation guarantee Willcoll makes
  owner_type account_owner_kind NOT NULL,  -- 'tenant' | 'property_till' | 'landlord_payable' | 'deposit_holding'
  owner_id UUID NOT NULL,                  -- tenant_id, property_id, or landlord_id depending on owner_type
  currency TEXT NOT NULL DEFAULT 'KES',
  balance NUMERIC(14,2) NOT NULL DEFAULT 0,   -- cache; source of truth is SUM(entries)
  created_at TIMESTAMPTZ,
  UNIQUE (owner_type, owner_id, currency)
)
```

Four account kinds per property, matching the paper reality:
- `tenant` — a **receivable**: positive balance = tenant owes money (arrears), mirrors the "arrears" column on the rent-roll sheet.
- `property_till` — cash/M-Pesa actually collected and sitting with the Manager, awaiting remittance.
- `landlord_payable` — what's owed to the Landlord once the Manager remits (rent minus management fee, if any).
- `deposit_holding` — ring-fenced deposits, never mixed with rent revenue (so a deposit refund at move-out is never accidentally paid from this month's rent till).

### 4.2 Entries (append-only, never updated or deleted)

```sql
ledger_entries (
  id UUID PK, account_id FK ledger_accounts NOT NULL,   -- organization_id inherited via account_id
  amount NUMERIC(14,2) NOT NULL,     -- positive = credit to this account, negative = debit
  transfer_id UUID NOT NULL,         -- groups the paired/multi-leg entries of one transfer
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)
-- No UPDATE or DELETE grant on this table for the application DB role, only INSERT + SELECT.
-- Reversals are new entries with transfer_type='reversal', never a DELETE — exactly the
-- receipt-book rule: you never tear out entry #090, you write a new one that cancels it.
```

```sql
ledger_transfers (
  id UUID PK, organization_id FK organizations NOT NULL,   -- carried directly, same rationale as ledger_accounts
  transfer_type transfer_kind NOT NULL,   -- rent_payment | water_payment | deposit_payment |
                                                        -- deposit_refund | remittance_to_landlord |
                                                        -- late_fee_charge | reversal
  invoice_id FK invoices,             -- nullable (remittances have no invoice)
  method payment_method NOT NULL,     -- cash | mpesa_manual | bank | cheque | intasend_mpesa | intasend_card
                                        -- the intasend_* methods are tenant-initiated and posted by the
                                        -- webhook handler rather than typed in by a Manager/Agent (§5.5);
                                        -- mpesa_manual stays for a Manager keying in an M-Pesa code that
                                        -- came in outside the app (till number paid directly, etc.)
  reference TEXT,                     -- M-Pesa code / cheque no. — the "with thanks" line
  narrative TEXT,                     -- "being payment of ... rent" free text, kept for audit readability
  idempotency_key TEXT UNIQUE NOT NULL,  -- guards against double-submission (double-tap, offline replay)
  recorded_by FK users NOT NULL, created_at TIMESTAMPTZ
)
```

### 4.3 `internal/money` — the only code allowed to write these tables

Following simplebank's transfer pattern exactly, each of these is **one method on `money.Service`, one DB transaction, `REPEATABLE READ`, consistent ascending-ID lock order** on the accounts touched — `internal/money` exposes a single `Service` struct (constructed once via `NewService(pool)` and injected into every handler/service that needs it), and every row below is `Service.<Name>`, never a bare package-level function (see the codebase's OOP conventions in the README and build guide):

| Method | What it posts |
|---|---|
| `ExecuteRentPaymentTx` | Debit `tenant` account, credit `property_till`; updates the matching `invoice.amount_paid` |
| `ExecuteWaterGarbageTx` | Same shape, against the `water_garbage` invoice for that meter-reading month |
| `ExecuteDepositTx` | Debit tenant (as an obligation cleared), credit `deposit_holding` — never touches `property_till` |
| `ExecuteDepositRefundTx` | Debit `deposit_holding`, credit tenant/cash-out, on move-out |
| `ExecuteLateFeeChargeTx` | Credit `tenant` account (increases what they owe) — computed nightly from `late_fee_pct_per_day × days_late × rent`, matching Neema House clause 4 |
| `ExecuteRemittanceTx` | Debit `property_till`, credit `landlord_payable` (and clears it to zero when the Manager physically pays the Landlord out) |
| `ReverseTransferTx` | Posts the mirror-image entries of a prior transfer, tagged `reversal`, linked via `reversed_transfer_id` — used instead of ever mutating a wrong entry |

Every one of these takes an `idempotency_key` (e.g., `sha256(property_id, unit_id, period_month, invoice_type, amount)` for a routine rent capture, or a client-generated UUID from the UI's "Record Payment" button) so a flaky connection or a double-tap on a shared tablet can never double-post — the same guard RipplePOS uses at its till. For a tenant-initiated IntaSend payment, the caller is `internal/payments`' webhook handler rather than a UI click, and the key is derived from `payment_gateway_transactions.gateway_reference` — see §3.1b and §5.5. Every `Execute*Tx` runs inside the same request-scoped transaction that already has `app.current_org_id` set (§1a) — RLS is therefore a second, database-enforced check underneath `internal/money`'s own logic, not a replacement for it: even a hypothetical bug that let one Organization's `unit_id` leak into another's request would still be rejected at the row level.

### 4.4 Reconciliation view

`v_unit_statement(unit_id, period_month)` reproduces exactly the "current monthly rent / arrears in previous month recovered / amount paid" columns from the SABINA/NGONG sheet, computed from `ledger_entries` — **never stored**, so it can never drift from the ledger. Like every other query in the system, it runs inside the caller's RLS-scoped transaction, so it is inherently Organization-scoped without an explicit filter.

---

## 5. Money flows, mapped to the collection models

### 5.1 Rent
Manager (or a permitted Agent) taps **Record Payment** on a unit → `ExecuteRentPaymentTx` → invoice updated → tenant SMS "Rent received: KES X for [Month]" → Landlord gets an SMS only if it crosses their configured digest (real-time for large payments, or a daily digest — Landlord preference, §2).

### 5.2 Water & garbage
Manager enters the current meter reading once a month (mirrors the physical ledger exactly: current, previous auto-pulled, units computed, amount computed) → system creates the `water_garbage` invoice → payment recorded the same way as rent, or bundled into one "Record Payment" screen that splits a single M-Pesa amount across rent + water + arrears (mirrors "Rent Deposit / Bill / Water Bill" fields on the Kiwi Place slip) using an explicit **payment allocation** rule: arrears first, then current water/garbage, then current rent, unless the Manager manually overrides the split.

### 5.3 Deposits
Captured once at lease creation (`ExecuteDepositTx`), held in `deposit_holding`, shown on the unit's statement as a separate line — never available for "remittance to landlord" until refunded/forfeited at move-out.

### 5.4 Electricity — tenant self-service, deliberately outside the ledger
Electricity is prepaid/token-based in the Kenyan market (KPLC tokens). Willcoll does not bill, collect, or remit electricity money — it only gives the tenant an SMS-based/USSD-style self-service log for their own record:

```sql
electricity_topups (   -- informational only — no ledger_entries reference this table
  id UUID PK, unit_id FK units NOT NULL,   -- organization_id inherited via unit_id
  reported_by tenant_id FK tenants,   -- self-reported via inbound SMS
  amount NUMERIC(10,2), token_reference TEXT, reported_at TIMESTAMPTZ
)
```

This keeps the ledger honest: only money that actually passes through the Manager's hands is ever in `ledger_entries`.

### 5.5 Tenant self-pay via IntaSend — M-Pesa & card, webhook-driven

Rent and water/garbage can still be collected the fully manual way (§5.1–§5.2, a Manager/Agent typing in what a tenant handed over or paid to a till). Additively, once a Manager has completed payout onboarding (§2.4, §3.1b), tenants can pay **directly**, without any Manager involvement in the moment:

```
[Tenant taps "Pay Rent" SMS link / USSD-style prompt]
        │
        ▼
[POST /v1/payments/initiate] ── creates a PENDING payment_gateway_transactions row,
        │                        calls IntaSend's Collection API (STK push to the tenant's
        │                        MSISDN for M-Pesa, or a hosted checkout link for card),
        │                        tagging the checkout with unit_id + invoice_id + a
        │                        client-generated idempotency reference in the metadata
        ▼
[Tenant approves on their phone / enters card details]
        │
        ▼
[IntaSend] ──(HTTP POST webhook, the split-second the payment clears)──▶ [POST /v1/webhooks/intasend]
        │
        ▼
Webhook handler: verify signature → upsert payment_gateway_transactions by
(gateway_provider, gateway_reference) → if COMPLETE and not already posted →
ExecuteRentPaymentTx / ExecuteWaterGarbageTx (routed to the correct Manager's
sub-account, §3.1b) → invoice updated → tenant SMS receipt → Landlord digest per §5.1
```

Willcoll's backend never polls IntaSend for payment status — it relies entirely on this webhook, matching the general pattern of any modern payment integration: real-time HTTP callbacks instead of continuously checking the gateway API. Because a webhook can legitimately arrive more than once (retries, network blips), and a tenant can legitimately double-tap "Pay Now," **the `payment_gateway_transactions` unique constraint plus the ledger's own idempotency key (§3.1b) are what actually make duplicate posting impossible** — not "best effort" client-side debouncing.

Settlement itself — IntaSend paying the Manager's actual bank account/wallet — happens on IntaSend's own payout schedule, outside Willcoll's ledger; `manager_payout_accounts.gateway_subaccount_id` is what tells IntaSend which Manager's bank details to settle to, so Willcoll's system never sees or touches the Manager's bank credentials directly, only the opaque sub-account ID.

---

## 6. API design — Alex Edwards conventions applied

Same shell as RipplePOS: `httprouter`, hand-rolled middleware chain, PASETO auth, structured JSON logs, panic recovery, background dispatch (SMS instead of email-as-primary), graceful shutdown, `/debug/vars`, Caddy in front. One middleware is unique to a multi-tenant deployment and sits closest to the database of any of them — see `middleware_tenant.go` in §6.2.

### 6.1 Route table (representative, not exhaustive)

```
POST   /v1/auth/register-manager          Manager self-signup — creates a NEW organizations row and its
                                           first Manager in one transaction, sets password immediately,
                                           activated=true (§1a, §3.1)
POST   /v1/auth/login                     Manager/Agent/Landlord password login — issued PASETO carries
                                           organization_id + role + user_id as claims (§3.1a)
POST   /v1/auth/refresh

POST   /v1/agents                         Manager only → creates invited Agent in the SAME organization_id
                                           as the calling Manager, emails activation link (internal/mailer,
                                           activation-password.tmpl, scope='activation')
PUT    /v1/users/activate                 {token, password} → Agent's first-ever password set;
                                           activated=true, status='active', token consumed
POST   /v1/agents/:id/reset-password      Manager only → invalidates prior reset tokens, emails a fresh
                                           reset link (reset-password.tmpl, scope='password-reset')
PUT    /v1/users/password                 {token, new_password} → Agent consumes reset token, sets new
                                           password, all of that Agent's sessions revoked

GET    /v1/properties                     scoped to caller — RLS (organization_id, §1a) filters first,
                                           RBAC+PBAC filters second, both server-side
POST   /v1/properties                     Manager/Landlord
GET    /v1/properties/:id/units
POST   /v1/properties/:id/units
POST   /v1/properties/:id/tenants/import  Bulk CSV import of units + sitting tenants + leases (§3.2a) —
                                           Manager/Agent (if can_edit_leases); 409s if the property has no
                                           property_ownership row yet (a Landlord must exist first)
POST   /v1/units/:id/leases               create lease (move-in) — also posts the deposit transfer
POST   /v1/leases/:id/terminate           move-out flow — triggers deposit refund/forfeit decision

POST   /v1/meters/:id/readings            record monthly reading → generates water_garbage invoice
GET    /v1/units/:id/statement            the v_unit_statement view, paginated by month

POST   /v1/payments                       ExecuteRentPaymentTx / ExecuteWaterGarbageTx (type in body) —
                                           manual capture by Manager/Agent (cash/mpesa_manual/bank/cheque)
POST   /v1/payments/:id/reverse           ReverseTransferTx — Manager or permitted Agent only
POST   /v1/remittances                    Manager → ExecuteRemittanceTx to a Landlord

POST   /v1/payments/initiate              Tenant-facing (no login — reached via a signed SMS link/USSD
                                           prompt) → starts an IntaSend STK push (M-Pesa) or hosted card
                                           checkout for a specific invoice; requires the Manager to have
                                           a verified payout sub-account (§2.4)
POST   /v1/webhooks/intasend              IntaSend → Willcoll only. Resolves organization_id from the
                                           checkout metadata (§3.1b), verifies the IntaSend signature,
                                           upserts payment_gateway_transactions by gateway_reference
                                           (unique constraint = duplicate-delivery guard), and on a first-
                                           time COMPLETE status posts the matching ExecuteRentPaymentTx /
                                           ExecuteWaterGarbageTx (§5.5)

POST   /v1/managers/payout-account        Manager submits bank details + KYC docs → creates/updates
                                           manager_payout_accounts (kyc_status='pending')
GET    /v1/managers/payout-account        Manager checks their own KYC/sub-account status
POST   /v1/managers/payout-account/verify Internal/admin-triggered once IntaSend confirms KYC clearance →
                                           sets gateway_subaccount_id, kyc_status='verified'

POST   /v1/organization/managers          Manager only → invites a SECOND Manager into the same
                                           Organization (§1a) — same activation-link mechanism as an Agent,
                                           role='manager' instead of 'agent'
GET    /v1/organization                   Any authenticated user in the org → name, subscription_tier,
                                           billing_status, live unit-count-to-tier progress (§12), plus the
                                           five public branding fields below (brand_name, logo_url, etc.)
PATCH  /v1/organization                   Manager only (super-manager gated) → name/slug
PATCH  /v1/organization/branding          Manager only (super-manager gated) → brand_name, logo (multipart
                                           upload), accent_key (must be one of §1b.3's fixed keys),
                                           sms_sender_id, email_from_name (§1b)
GET    /v1/public/branding?slug=          UNAUTHENTICATED — the five public columns only (§1b.1), used by
                                           the Nuxt app to paint /login before any session exists (§1b.2)

GET    /v1/reports/arrears?property_id=
GET    /v1/reports/collections?property_id=&month=
GET    /v1/reports/portfolio              Landlord-scoped consolidated view

POST   /v1/properties/:id/score           Manager only, premium-gated (organization_addons, §3.5, §12.3) →
                                           internal/scoring calls the ml-sidecar (§13.4) for THIS property
                                           only, persists a new property_scores row, returns it
GET    /v1/properties/:id/score           Latest property_scores row for this property (Manager or the
                                           owning Landlord, read-only — §13.4)
GET    /v1/properties/:id/score/history   Full property_scores timeline for this property, paginated

POST   /v1/organization/addons/verified-property-score/activate    Manager only → activates the premium
                                           add-on for the whole Organization (§12.3); billed the same
                                           recurring way as the base subscription (§12.1), not a separate
                                           gateway mechanism
POST   /v1/organization/addons/verified-property-score/cancel      Manager only

POST   /v1/agent-grants                   Manager sets/updates an Agent's PBAC bitset for one property
GET    /v1/audit-log?property_id=

GET    /v1/healthz  /v1/readyz  /debug/vars
```

### 6.2 Cross-cutting conventions (Let's Go Further-style)
- **`middleware_tenant.go` (multi-tenancy, §1a)** — runs immediately after auth, before permissions: reads `organization_id` off the verified PASETO token and executes `SET LOCAL app.current_org_id = '<uuid>'` as the first statement of the request's DB transaction, so every RLS-protected table (§1a, §3–§4) is automatically scoped for the rest of the handler — no query in the codebase needs its own `WHERE organization_id = ?`. `/v1/webhooks/intasend` and `/v1/auth/register-manager` are the only two routes that don't go through this in the normal way, since neither has an existing Organization to scope to yet (§3.1b, §6.1) — both are individually audited exceptions, not a general escape hatch.
- **Enveloped JSON**: `{"data": ...}` / `{"error": "..."}`, matching ch.3–4.
- **Hand-written validation** per endpoint (no struct-tag binding library): KES amounts (positive, 2dp), Kenyan MSISDN format, meter-reading monotonicity (`current_reading >= previous_reading`), unit-label uniqueness per property.
- **Permission middleware** (`middleware_permissions.go`) runs AFTER `middleware_tenant.go` (§6.2 above) and checks role **and** — for Agents — the `agent_property_grants` row for the `:property_id`/`:unit_id` in the path, denying with a clear "not granted for this property" message rather than a generic 403.
- **Rate limiting**: per-IP on `/auth/*`, per-user token-bucket on `/payments` to blunt accidental double-submit storms.
- **Structured JSON logs + panic recovery** on every request; every write endpoint logs `actor_id, organization_id, role, property_id, transfer_id` for the audit trail — `organization_id` is included even though RLS already scopes the query, because logs are the first thing a support engineer greps when a customer reports "I see the wrong data."
- **Background dispatch workers** (WaitGroup-drained on graceful shutdown, ch.14 pattern) — **two** independent dispatchers, since Agents/Managers/Landlords communicate by email and Tenants by SMS:
  - `internal/notify` (SMS): tenant rent-received/rent-due/arrears, Landlord SMS digest.
  - `internal/mailer` (SMTP): Agent activation link, Agent password-reset link (Manager-initiated), both via Mailtrap in dev/staging and a real SMTP provider in production — same interface, config-swapped.
- **Idempotency-Key header** required and enforced at the DB unique-constraint level on `/v1/payments` and `/v1/remittances`.
- **Webhook security** on `/v1/webhooks/intasend`: signature verification against IntaSend's shared secret before any DB write, request logged with `gateway_reference` regardless of outcome, and the endpoint is excluded from the normal PASETO-auth middleware chain (IntaSend is not a logged-in user) but still passes through structured logging, panic recovery, and its own dedicated rate limit.

---

## 7. Backups & deployment

Mirrors the RipplePOS hosting model exactly, since it's proven and cheap to run:

| Layer | Choice |
|---|---|
| Hosting | Contabo VPS |
| Reverse proxy / TLS | Caddy — also serves the static Nuxt export |
| Containers | Docker Compose, 7 services: `postgres`, `migrate` (one-shot), `api`, `web`, `caddy`, `pg_backup`, `ml-sidecar` (§13.3 — FastAPI, CPU-only, no exposed public port, reached only server-to-server from `api`). Multi-stage, multi-target Dockerfiles (`build/package/Dockerfile.api`, `Dockerfile.web`, `ml-sidecar/Dockerfile`) — a `dev` target with hot-reload (air for Go, Nuxt's own dev server for the frontend, `uvicorn --reload` for the sidecar) and a `prod` target producing a minimal non-root static binary / nginx-served static export / non-root Python image respectively. `build/docker-compose.yml` is dev-ready out of the box; `build/docker-compose.prod.yml` layers on top for the VPS (prod targets, no source bind mounts, no exposed DB/API/sidecar ports, `restart: always`, capped log files) |
| Restore | `make restore FILE=<dump>` runs `pg_restore` against the running `postgres` container from a file in the `pgbackups` named volume — deliberately a manual, explicit command rather than anything automatic, since restoring over live ledger data is a one-way door. Because Willcoll is a shared-schema multi-tenant deployment (§1a), a full restore brings back **every** Organization's data as of that snapshot, not just one customer's — a single-Organization "restore just them" is not offered in v0.1 (tracked as an open question, §11) |
| Backups | A dedicated `pg_backup` container (`build/package/Dockerfile.backup`), independent of api/web health, runs `scripts/backup/nightly-pg-dump.sh` on a cron schedule: custom-format `pg_dump`, optional GPG encryption, optional offsite push via `curl -T` to any endpoint, and local retention pruning. Postgres itself is configured with `archive_mode=on` and `archive_command` pointed at `scripts/backup/wal-archive.sh`, giving continuous WAL archiving into a separate named volume — so the ledger is recoverable to any point in time via `pg_restore` of the nightly dump plus WAL replay, not just to last night's snapshot. Non-negotiable given §4's "never edit, only append" guarantee would otherwise be meaningless if a bad night wiped the disk |
| Migrations | golang-migrate, forward-only in production (no down-migrations run against live money tables without a DBA sign-off). The `organization_id` + RLS migration (§1a, §3.1) ships in the very first migration, `000001_init.up.sql`, not bolted on later — retrofitting RLS onto a schema already carrying customer data is materially riskier than shipping it from the start |
| Monitoring | `/debug/vars` + a simple uptime check on `/healthz`; a daily automated `SELECT` reconciling `ledger_accounts.balance` cache against `SUM(ledger_entries.amount)` per account, alerting the super-manager by SMS if any account ever drifts. Run under the `platform_admin` role (§1a), this reconciliation job is one of the few things in the system that legitimately queries across every Organization at once |
| Outbound email | SMTP — **Mailtrap** in dev/staging (catches every activation/reset email in a sandbox inbox so nothing leaks to a real address during testing), a production SMTP provider (e.g. Postmark/SES/SendGrid SMTP) behind the same `internal/mailer` interface in production. Config via Viper: `smtp.host`, `smtp.port`, `smtp.username`, `smtp.password`, `smtp.sender` |
| Payment gateway | **IntaSend** — one master Willcoll business account, Managers provisioned as sub-accounts once KYC clears (§2.4, §3.1b). Config via Viper: `intasend.publishable_key`, `intasend.secret_key`, `intasend.webhook_secret`, `intasend.environment` (sandbox in dev/staging, live in production — never live keys outside the production `.env`) |
| CI/CD | Two GitHub Actions workflows: `.github/workflows/test.yml` (`go vet`, `go test ./... -race` including `internal/money`'s concurrency tests, `web/app/`'s lint and static-export build, and `ml-sidecar/`'s pytest suite including the single-property-isolation test, §13.3.1) on every PR/push; `.github/workflows/deploy.yml` (build/push the `build/package/` and `ml-sidecar/` Docker images, SSH to the Contabo VPS, run `scripts/deploy.sh` which migrates and restarts `build/docker-compose.yml`) on merge to `main` |

---

## 8. Monorepo layout

The Go backend follows the **Standard Go Project Layout** (`golang-standards/project-layout`) rather than a bespoke `backend/` folder, so that `/cmd`, `/internal`, and `/pkg` carry their conventional meaning. Full annotated tree: [`project-structure.txt`](project-structure.txt); summarized here:

```
willcoll/
├── cmd/
│   ├── willcoll/main.go              API server entrypoint
│   └── willcoll-seed/main.go         standalone seed/import CLI
├── internal/                         private business logic — not importable outside this module
│   ├── api/                          handlers_*, middleware_*, router.go, validate.go
│   ├── domain/                       property.go, unit.go, lease.go, tenant.go, meter.go,
│   │                                 invoice.go, ledger.go, user.go, audit.go
│   ├── money/                        ledger.go, transfer.go, locking.go, reconcile.go, transfer_test.go
│   │                                 (owns ledger_accounts.balance & invoices.amount_paid — nothing else touches them)
│   ├── billing/                      latefees.go — late-fee policy, separate from money's transfer mechanics
│   ├── payments/                     intasend_client.go (Collection API: STK push + card checkout),
│   │                                 webhook_handler.go (verifies signature, upserts
│   │                                 payment_gateway_transactions, calls internal/money on first-time
│   │                                 COMPLETE), payout_accounts.go (Manager KYC/sub-account onboarding)
│   ├── auth/                         paseto.go, password.go, permissions.go (RBAC+PBAC checks),
│   │                                 tokens.go (activation/password-reset token generation & validation)
│   ├── notify/                       sms.go (Africa's Talking / similar), templates/  — SMS to Tenants/Landlords
│   ├── mailer/                       smtp.go (Mailtrap dev / real SMTP prod), dispatcher.go, templates/
│   │                                 activation-password.tmpl, reset-password.tmpl — email to Managers/Agents
│   ├── db/                           sqlc-generated + query/*.sql, migrations/
│   └── config/                       Viper (env + /configs/*.yaml, incl. intasend.* keys)
├── pkg/                              public, Willcoll-agnostic code — safe to import elsewhere
│   ├── kesmoney/                     KES fixed-point money type
│   ├── msisdn/                       Kenyan phone-number validation
│   ├── idempotency/                  idempotency-key helper
│   ├── smsclient/                    provider-agnostic SMS gateway interface
│   └── intasendclient/               thin wrapper over IntaSend's Collection/Sub-Account REST API
├── test/                             cross-package integration/e2e tests + fixtures
│   ├── integration/                  payments_flow_test.go, meter_billing_flow_test.go, rbac_pbac_test.go
│   └── testdata/                     sample_unit_import.csv, fixtures.sql
├── configs/                          config.example.yaml, per-environment templates (no secrets committed)
├── docs/
│   ├── willcoll-spec.md              (this document)
│   └── db-diagram.dbml
├── examples/                         example .http API requests, blank unit-ledger CSV import template
├── api/
│   └── openapi.yaml                  OpenAPI 3.0 contract
├── web/                              web application root
│   └── app/                          Nuxt 4 / Vue 3 — ONE codebase, static export (ssr:false) — WORKING DIR
│       ├── pages/
│       │   ├── login.vue
│       │   ├── properties/index.vue          apartment switcher grid (branding + location + rent indicator card)
│       │   ├── properties/[id]/units/index.vue
│       │   ├── properties/[id]/units/[unitId].vue   unit statement, ledger timeline, record-payment drawer
│       │   ├── properties/[id]/meters.vue           monthly reading entry grid (mirrors the paper ledger)
│       │   ├── agents/index.vue                     invite + PBAC grant matrix editor
│       │   ├── reports/portfolio.vue                Landlord consolidated view
│       │   ├── settings/sms-templates.vue
│       │   └── settings/payout-account.vue           Manager's IntaSend KYC form + sub-account/verification status
│       ├── components/
│       │   ├── layout/AppSidebar.vue, AppHeader.vue
│       │   ├── property/PropertySwitcherCard.vue    logo/brand + location + live rent-collected indicator
│       │   ├── ledger/PaymentDrawer.vue, LedgerTimeline.vue
│       │   └── shared/StatusBadge.vue, RoleGate.vue  (renders by RBAC+PBAC, not just role)
│       ├── composables/useAuth.ts, usePermissions.ts, useLedger.ts
│       ├── assets/tokens.css              generated 1:1 from design-tokens.txt
│       └── dist/                          static export build output (git-ignored; Caddy serves this)
├── build/                            packaging: Dockerfiles, docker-compose.yml, Caddyfile, systemd unit
├── scripts/                          deploy.sh, backup/nightly-pg-dump.sh, backup/wal-archive.sh,
│                                     reconcile-check.sh, seed-from-csv.sh
├── vendor/                           optional `go mod vendor` output
├── ml-sidecar/                       Verified Property Score — FastAPI sidecar (§13), 7th Docker Compose
│                                     service, read-only Postgres role against the SAME instance, no
│                                     exposed public port — reached only server-to-server from
│                                     internal/scoring (§13.3); full annotated tree in project-structure.txt
└── .github/workflows/
    ├── test.yml                       go vet + go test ./... -race + web/app/ lint & build + ml-sidecar/
    │                                  pytest (single-property-isolation test, §13.3.1)
    └── deploy.yml                     build/push images (incl. ml-sidecar/Dockerfile) → SSH to Contabo
                                       VPS → scripts/deploy.sh
```

---

## 9. UI/UX — sidebar + flex/grid, driven by `design-tokens.txt`

### 9.1 Shell
- **Sidebar** (`--layout-sidebar-width: 260px`, `--sidebar-bg`, white with `--color-border-subtle` divider): Portfolio, Properties, Ledger, Reports, Agents & Permissions, Settings. Active item uses `--sidebar-item-active-bar` (3px orange) + `--sidebar-item-active-bg` tint — exactly the tokens supplied.
- **Header** (`--layout-header-height: 68px`): property switcher dropdown, current-month collection-rate chip, notification bell for arrears alerts.
- **Canvas** (`--color-bg-canvas`) holds a CSS Grid of `--layout-card-radius: 10px` cards on `--shadow-card`.

### 9.1a Unauthenticated / auth-flow pages (no sidebar layout)
Three pages render outside the sidebar shell, using a centered single-card layout on `--color-bg-canvas`:
- **`pages/login.vue`** — email + password, used by Manager, Agent (post-activation), and Landlord.
- **`pages/activate.vue?token=...`** — Agent's first-ever password-set screen, reached only from the emailed activation link; on success, redirects to `/login`. Shows a clear "invalid or expired link — ask your Manager to resend" state if the token fails validation, rather than a raw error.
- **`pages/reset-password.vue?token=...`** — Agent's new-password screen, reached only from the Manager-triggered reset email; on success, redirects to `/login`. Same expired/invalid-token handling as above.

### 9.2 Multi-apartment management (the Manager's home screen)
A **CSS Grid of property cards**, one per apartment the Manager runs — this is the answer to "manage multiple apartments simultaneously with proper branding, location, and a rent indicator":

```
grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
gap: var(--spacing-lg);
```

Each `PropertySwitcherCard`:
- Property name + a small brand mark (logo upload per property, falling back to the Organization's own `logo_url`, §1b — never a hardcoded Willcoll mark)
- Location line (from `properties.location`)
- **Rent indicator** — a horizontal bar/ring using `--color-status-success-*` / `--color-status-warning-*` / `--color-status-error-*` for `collected / expected` this month (e.g. "82% collected — 6 units in arrears"), computed live from `v_unit_statement`
- Click → `properties/[id]/units` (flex list, each unit row: label, tenant name, status badge using `--color-status-*` tokens, balance, quick "Record Payment" button)

### 9.3 Unit detail
Flex layout: left column = lease/tenant/meter info card; right column = `LedgerTimeline` (a vertical, append-only feed — visually reinforcing "you cannot edit history, only add to it," echoing the receipt-book metaphor) + `PaymentDrawer` for new entries.

### 9.4 Meter-reading entry screen
Deliberately shaped like the paper ledger it replaces: one row per unit, columns **Previous | Current | Units | Amount** (last two computed live, greyed out, non-editable — matches the generated columns in §3.3), a single "Post all readings" action that fires one invoice-creation call per row.

### 9.5 Agents & Permissions screen
A matrix table: rows = Agents, columns = properties the Manager owns, each cell a popover checklist of the six PBAC toggles from §2.2 — the Manager can see and edit an Agent's entire footprint across every property in one screen.

### 9.6 Role-driven rendering
`RoleGate` wraps any action button/column and checks role **and**, where relevant, the current property's grant row — so an Agent with `can_record_payments=false` on Property 2 simply never sees the "Record Payment" button there, without any client-side permission logic being trusted server-side (the API middleware is the real gate; the UI hides what it doesn't expect to be allowed to do).

---

## 10. User lifecycle, end to end

| Step | Actor | What happens |
|---|---|---|
| 0 | Manager | Signs up directly and sets their own password in the same request → `users` row created with `activated=true`, `status='active'` immediately. No email step — a Manager can log in the moment they finish signing up |
| 1 | Landlord | Signs up (or is onboarded by a Manager they've engaged) → account created with role `landlord`, linked to their property via `property_ownership` |
| 2 | Manager | Creates the property (name, location, rates) and its units, one at a time or via CSV import mirroring the paper ledger columns |
| 3 | Manager | Creates a lease for a unit: enters tenant (name + phone), rent, deposit → `ExecuteDepositTx` fires, unit flips to `occupied`, tenant gets a welcome SMS with rent-due-day and Manager contact |
| 4 | Manager/Agent | Monthly: enters meter reading → invoice generated → records rent/water payments as they come in → tenant gets an SMS receipt per payment, matching "With thanks — For Jane Wairimu Mwangi" |
| 5 | System (nightly job) | Computes late fees for any invoice past `rent_due_day` per the lease's `late_fee_pct_per_day`, posts `ExecuteLateFeeChargeTx`, sends an arrears SMS to the tenant and (if over the Landlord's alert threshold) to the Landlord |
| 6 | Manager | `POST /v1/agents` invites an Agent → account created `activated=false`, `status='invited'` → `internal/mailer` sends an activation email (`activation-password.tmpl`, SMTP/Mailtrap) with a single-use, 3-day token |
| 6a | Agent | Opens the email → clicks the activation link → lands on `pages/activate.vue` → sets their password for the first time (`PUT /v1/users/activate`) → `activated=true`, `status='active'` → **redirected to `/login`** |
| 6b | Agent | Logs in with the email + password just set → Manager then grants PBAC per property from the Agents screen |
| 6c | Manager | Later, if an Agent is locked out: clicks **"Reset password"** on the Agents screen → `POST /v1/agents/:id/reset-password` → `internal/mailer` sends a fresh reset email (`reset-password.tmpl`) with a single-use, 45-minute token — the Manager never sees or sets the new password |
| 6d | Agent | Clicks the reset link → `pages/reset-password.vue` → sets a new password (`PUT /v1/users/password`) → all of their existing sessions are revoked → **redirected to `/login`** |
| 7 | Manager | Periodically remits collected rent to the Landlord → `ExecuteRemittanceTx` clears `property_till` into `landlord_payable`, then marks it settled once physically paid out; Landlord gets a remittance-confirmation SMS |
| 8 | Tenant | Move-out: Manager terminates the lease → inspects unit → `ExecuteDepositRefundTx` (full/partial per any deductions) → unit flips back to `vacant`, ledger keeps the full historical trail forever |
| 9 | Agent (if revoked) | Manager sets `revoked_at` on the grant — access to that property disappears immediately on next request, account itself stays alive for other properties they still hold grants on |
| 10 | Landlord | Anytime: views their consolidated portfolio report; receives only SMS alerts, never logs into an operational screen unless they choose to check the read-only dashboard |

---

## 11. Open questions (tracked, not blocking v0.1)

1. Payment allocation order (arrears vs. current-month split) — default proposed in §5.2; should be a per-property configurable rule.
2. Management-fee model: does `ExecuteRemittanceTx` deduct a fixed % before crediting `landlord_payable`, or is that a separate invoice to the Landlord? Needs a business decision before `internal/money` is finalized.
3. SMS gateway vendor (Africa's Talking vs. a bank-grade aggregator) — affects `internal/notify` interface shape but not the schema. Production SMTP provider (Postmark/SES/SendGrid) for `internal/mailer` is a separate, independent choice — Mailtrap is dev/staging only and must never be reachable from production config.
4. Multi-currency: out of scope for v0.1 (KES only), matching the paper ledgers.
5. IntaSend transaction fees: whether the gateway's per-transaction fee (M-Pesa vs. card differ) is absorbed by Willcoll, passed to the Manager, or passed to the tenant at checkout — needs a business decision before `/v1/payments/initiate` is finalized.
6. KYC turnaround SLA: how long a Manager's `manager_payout_accounts` row is expected to sit in `kyc_status='pending'` before Willcoll follows up with IntaSend or the Manager — not a technical blocker, but affects onboarding-flow copy/expectations.
7. Whether Tier boundaries in §12 count *occupied* units only or all units ever created (including vacant/never-let units) — affects how "unit count" is computed for billing.
8. Custom domains per Organization (§1b.4) — whether a growing Organization can eventually get `app.theirbrand.co.ke` instead of a shared-domain slug, and if so, how Caddy's automatic TLS (§7) is extended to provision per-tenant certificates without manual VPS steps per customer.

---

## 12. Pricing & business model

Willcoll monetizes on two independent layers: a predictable SaaS subscription paid by the Manager, and a payment-processing flow where money never touches Willcoll's own balance sheet (IntaSend settles straight to each Manager's sub-account, §3.1b).

### 12.1 Component A — the fixed monthly software fee (SaaS)

Property Managers are billed a low monthly subscription based on the number of units they onboard, giving Willcoll a predictable baseline revenue independent of collection volume:

| Tier | Units | Monthly fee (KES) |
|---|---|---|
| Tier 1 — Starter | 1 – 15 units | 1,500 |
| Tier 2 — Growth | 16 – 50 units | 3,500 |
| Tier 3 — Professional | 51 – 150 units | 7,500 |
| Tier 4 — Enterprise | 151+ units | 12,000+ (custom quote above the floor) |

The Manager's subscription tier is computed from `SUM(units)` across every property they administer (`properties.manager_id`), re-evaluated whenever a unit is added — crossing a tier boundary upgrades the subscription at the next billing cycle, not mid-cycle. This subscription is billed to the Manager directly by Willcoll (a separate billing relationship from IntaSend, which only ever moves tenant-to-Manager rent money) — mechanism (invoice vs. its own recurring IntaSend/card charge) is an implementation detail for a future revision, not specified here.

### 12.2 Component B — payment processing (IntaSend)

Digital tenant payments (§5.5) move through IntaSend's standard M-Pesa/card processing fees, deducted by IntaSend before settlement to the Manager's sub-account; Willcoll does not currently take a spread on top of IntaSend's fee (see open question §11.5). Manual collection (cash, till-paid M-Pesa, bank, cheque — §5.1–§5.2) carries no processing fee at all, since no gateway is involved.

### 12.3 Component C — Verified Property Score (premium add-on)

The AI layer (§13) is **not** included in Component A's base tiers — it's an opt-in add-on, billed the **same way** the base subscription is (a recurring Organization-level charge, §12.1), never through a second gateway mechanism or a per-score micro-charge. A Manager activates it via `POST /v1/organization/addons/verified-property-score/activate` (§6.1), which creates/updates the `organization_addons` row (§3.5) with `status='active'`; deactivation is symmetric. While active, every Manager in that Organization can request a score for any property they administer, subject to the same RBAC/PBAC scoping (§2) as everything else. The add-on is priced flat per-Organization rather than per-property or per-score-computation, so a Manager scoring five buildings in one month costs the same as scoring one — the value being sold is *access to the capability*, matching how Component A already bills by portfolio size rather than by transaction count. Exact KES figure is left open pending the same kind of business decision noted for Component A/B in §11 — not specified here.

---

## 13. The AI layer — Verified Property Score

### 13.0 The value shift this layer is built to unlock

Everything in §1–§12 turns a paper receipt book into an immutable, auditable ledger — that alone is already the product. §13 is what that ledger makes possible *next*: because `ledger_entries` (§4.2) can never be retroactively edited or inflated, a specific property's rent-collection history becomes something a bank can actually underwrite against, the same way [AFB's Weza Tele](https://weza.afbgroup.co.ke) turned trustworthy, real transaction visibility across many SMEs' supply chains into something a lender could plug into directly. Willcoll applies that same logic to one asset class — individual rental buildings — instead of retail supply chains. The **Verified Property Score** is the mechanism: it converts one property's raw, tamper-proof rent-collection logs into a standardized, lender-ready credit metric. It is not a chatbot and it does not generate qualitative commentary — it is an algorithmic credit-underwriting engine that outputs the same numeric artifacts (NOI, DSCR, a score band) a bank's own risk team already produces by hand, just computed from data that can't be forged.

### 13.1 Model — deliberately small, deliberately boring

A **gradient-boosted decision tree (GBDT) ensemble**, sub-MB on disk, CPU-only, trained offline (`ml-sidecar/ml/train.py`, `project-structure.txt`) and loaded once at process startup (`app/core/model_loader.py`). No LLM, no GPU dependency, no `torch`/`tensorflow` in `pyproject.toml` — a conscious choice, not a v0.1 shortcut to be revisited later: the value this layer sells is *trustworthy input data*, not a cleverer model. A simple, auditable, fast numeric classifier is the right tool once the numbers feeding it are already true; a larger model would add inference cost and opacity without adding anything a lender would pay more for.

### 13.2 What it computes, for one property

For a single `property_id` (never a batch, never cross-property), the sidecar:
1. **Extracts features** from that property's own `ledger_entries`/`invoices`/`leases`/`meter_readings` history (§3–§4) — actual rent collected per unit over time, vacancy periods (`units.status` transitions), and arrears-recovery speed (time from an invoice going `open`/`partially_paid` to `paid`, per unit).
2. **Computes institutional risk metrics** from that same feature set — **Net Operating Income (NOI)** and **Debt Service Coverage Ratio (DSCR)** — using the exact formulas a commercial bank's own underwriting team already uses, so the output slots into an existing loan-evaluation process rather than requiring a lender to learn Willcoll's own metric.
3. **Runs the GBDT ensemble** over the feature vector to produce a raw `score_value`, then maps it to a **standardized `score_band`** (§3.5) — the same shape banks already use internally, presented in the format they already expect.
4. **Persists** the result as a new, append-only `property_scores` row (§3.5) — never overwriting a prior score, exactly mirroring `ledger_entries`' own "never edit, only append" guarantee (§4.2), so a Landlord's or lender's view of a property's score is itself an auditable timeline, not a single number that could quietly change.

### 13.3 Architecture — a sidecar, not a second service to operate

The **`ml-sidecar/`** FastAPI application (full annotated tree in `project-structure.txt`) runs **alongside** the main Go API in the same Docker Compose stack (§7) — a seventh service (`ml-sidecar`) added to the six already listed, sharing the same Postgres instance and the same `docker compose up` / `deploy.sh` lifecycle, so there is no separate deployment pipeline, no separate on-call surface, and no GPU fleet to provision. `internal/scoring` (a new Go package, following the `Service`-struct convention of every other `internal/*` package, §"Code paradigm") is the **only** caller: it holds an HTTP client configured with the shared secret (`SIDECAR_SHARED_SECRET`, `.env.example`) and calls the sidecar server-to-server; the sidecar itself is never reachable from the public internet (`docker-compose.yml`, no exposed host port), only from `internal/api`'s own network.

**13.3.1 Data access — read-only, RLS still applies, single-property isolation enforced twice.** The sidecar's Postgres role is granted `SELECT`-only on `ledger_entries`, `ledger_accounts`, `invoices`, `leases`, `units`, `meter_readings` — it has no `INSERT`/`UPDATE`/`GRANT` on anything except `property_scores`, which it owns exclusively (mirrors `internal/money` owning `ledger_accounts.balance`, §4.3). Row-Level Security (§1a) still governs every query the sidecar runs, exactly as it does for the Go API; the sidecar's `app/dependencies.py` additionally requires an explicit `property_id` on every request (never an Organization-wide or cross-property query is even expressible in `app/repositories/ledger_repo.py`), so single-property isolation is enforced at two independent layers — the database's RLS policy and the sidecar's own query shape — not just documented as a rule someone has to remember. A representative join, illustrating that the schema supports this cleanly:

```sql
-- feature_extraction_service.py's core query: one property's full collection history, unit by unit
SELECT u.id AS unit_id, u.unit_label, u.status,
       i.period_month, i.invoice_type, i.amount_due, i.amount_paid, i.status AS invoice_status,
       le.entry_type, le.amount, le.created_at AS entry_at
FROM units u
JOIN leases l           ON l.unit_id = u.id
JOIN invoices i          ON i.lease_id = l.id
JOIN ledger_entries le   ON le.owner_type = 'unit' AND le.owner_id = u.id    -- §4.1's owner_id polymorphism
WHERE u.property_id = :property_id                                          -- the single-property boundary
ORDER BY u.unit_label, i.period_month, le.created_at;
```

No `organization_id` filter needs to appear explicitly in application code here — RLS (§1a) already scopes every one of these tables to the calling session's organization, exactly as it does everywhere else in the codebase; the query above only needs to narrow further, to one property within that already-scoped organization.

### 13.4 Who can request a score, and who can see it

Strictly inside the **existing** Landlord–Manager relationship already modeled by `property_ownership` (§2.3, §3.1) — no new consent flow, no cross-property or cross-Organization pooling, and a property's score only ever depends on its own ledger (§13.3.1). Concretely, per the RBAC table in §2.1:

| Action | Landlord | Manager | Agent |
|---|:---:|:---:|:---:|
| Request a new score computation | — | ✅ (premium-gated, §3.5, §12.3) | — |
| View the latest score / score history for an owned/administered property | ✅ (own properties) | ✅ | — |

A Landlord never triggers a computation themselves — consistent with §2's "Landlord is read-mostly, SMS/web read-only, never operates the software directly" — they only ever *see* the result, either on the web dashboard's Landlord-scoped portfolio view (§9, `reports/portfolio.vue`) or, when a Manager chooses to share it, as a downloadable report the Agency hands the Landlord as a value-add (§13.6). The Tenant actor is never involved anywhere in §13 — a property's income-reliability score carries no Tenant-identifying detail, only aggregate per-unit collection behavior.

### 13.5 Technical & privacy constraints, restated as invariants

- **Single-property isolation is structural, not a convention.** Every code path from `POST /v1/properties/:id/score` down to `ledger_repo.py`'s SQL takes exactly one `property_id`; there is no method signature anywhere in `ml-sidecar/` that accepts a list of properties or an `organization_id` alone.
- **No cross-Organization training or inference.** `ml/train.py` is an offline, manually-run script against anonymized/aggregated historical data prepared separately — it is never invoked from the request path, and the running service in `app/` never trains or fine-tunes on a live customer's data; the model a live request hits is always the fixed artifact in `ml/model.joblib`.
- **No additional consent required.** Because the score only ever reads data the Landlord and Manager already both have visibility into via `property_ownership` (§2.3), computing one doesn't cross a permission boundary that didn't already exist.
- **CPU-only, sub-MB model** (§13.1) — reinforced here as a privacy/ops constraint too: no external inference API call, no customer ledger data ever leaves the Willcoll-controlled Postgres instance and `ml-sidecar` container.

### 13.6 Stakeholder value

| Stakeholder | What §13 changes for them |
|---|---|
| **Landlord** (asset owner) | An auditable NOI track record and a bank-ready DSCR turn an informal rent roll into evidence a lender can act on — for refinancing, equity release, or a straightforward property loan — and the same auditable history supports a higher valuation at sale. |
| **Manager / letting agency** | The underlying immutable ledger (§4) already eliminates the fraud/missing-cash/retroactive-tampering risk of a paper receipt book; §13 turns that same data into a proprietary "Verified Property Score" report the Agency can offer Landlords as a client-retention and fee-justification value-add (§12.3), not just an internal ops improvement. |
| **Lender / commercial bank** | A standardized DSCR rooted in tamper-proof transaction history, in the exact format their own underwriting already expects, removes the expensive manual-audit step normally required to verify a borrower's claimed rental yield — the score is meant to plug directly into an existing loan-evaluation process. |

### 13.7 Non-disruptive by design

§13 changes nothing about how rent is actually collected (§5) or how Tenants are reached (SMS only, §"Actors"). It adds one opt-in, premium, Manager-triggered action on top of data the system was already capturing for §4's ledger — a Landlord who never enables the add-on, or a Manager who never activates it (§12.3), loses nothing else the platform already does.