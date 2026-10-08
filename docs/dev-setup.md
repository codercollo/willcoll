# Local dev setup (Windows, Git Bash)

From a fresh clone to signed in at <http://localhost:3001> (platform), landing in Rentals at <http://localhost:3000>. No Docker, no hot-reload
tooling: Go, Postgres, a Makefile, Node.

## What runs

| Piece | Port | Started by |
|---|---|---|
| Postgres (native, already running) | 5432 | you |
| platform-api (identity, sessions, billing, admin) | 4100 | `make dev/up` |
| rentals-api (portfolio, ledgers, the pay page) | 4000 | `make dev/up` |
| Fake Africa's Talking (SMS) + fake PayHero | 14901 / 14902 | `make dev/up` |
| **Platform frontend** (Nuxt): sign-in, signup, account, staff, modules, billing, Super Admin | **3001** | `pnpm dev` |
| **Rentals frontend** (Nuxt): the Rentals product pages and the public pay page | **3000** | `pnpm dev` |

**Which URL is which.** Two apps, one login:

| App | URL | What lives there |
|---|---|---|
| platform | <http://localhost:3001> | `/auth/*` (login, signup, activate, forgot/reset password, staff invite), `/` (your products as cards), `/account`, `/staff`, `/modules`, `/billing/<product>`, `/admin/*` (Super Admin) |
| rentals | <http://localhost:3000> | properties, payments, landlords, reports, the setup guide, `/pay/*` and the QR pages (no login) |

Open Rentals while signed out and you are redirected to
`http://localhost:3001/auth/login?return_to=<the Rentals page>`; after signing in you come
back to that page. `return_to` is honoured only for origins in the apps list below (anything
else is ignored, so there is no open redirect). The product switcher in the sidebar (and
"More" on a phone) lists the products your firm runs and links to Account and Billing in
platform.

Each app talks only to its own origin. Nuxt proxies `/_platform/v1/**` to platform-api (both
apps) and, in Rentals, `/v1/**` to rentals-api. The session cookie (`willcoll_session`) is
set by platform-api through either proxy and is host-only on `localhost`; browsers do not
scope cookies by port, so **one sign-in covers both apps** and rentals-api sees the login
platform-api made. There is no CORS or cookie-domain setup in development.

The app URLs are runtime config (`runtimeConfig.public.apps`), set for you by `pnpm dev`:
`NUXT_PUBLIC_APPS_PLATFORM` (`http://localhost:3001`), `NUXT_PUBLIC_APPS_RENTALS`
(`http://localhost:3000`); estates and waste are empty until they ship. In production they are
subdomains (e.g. `https://app.willcoll.co.ke`, `https://rentals.willcoll.co.ke`), and
platform-api's `SESSION_COOKIE_DOMAIN=.willcoll.co.ke` makes the cookie reach all of them.

## Prerequisites (once per machine)

- **Postgres 17** running locally, and a **superuser** you know the password of (normally
  `postgres`). `psql` must be on `PATH` (`C:\Program Files\PostgreSQL\17\bin`).
- **Go** (see `backend/go.mod`), **make** (GnuWin32 is fine), **Node 22** and **pnpm 11**.
- Optional: [`migrate`](https://github.com/golang-migrate/migrate) (`scoop install migrate`).
  Without it the scripts apply the `.up.sql` files with `psql`.

Check:

```bash
psql --version && go version && make --version | head -1 && node --version && pnpm --version
```

## 1. Configure `backend/.env`

```bash
cd backend
cp .env.example .env
```

Edit **three** lines in `backend/.env`:

- `MIGRATE_DSN` — put your Postgres superuser password in (`postgres://postgres:<password>@localhost:5432/willcoll?sslmode=disable`).
  Only host, port, user and password are used; the database name in it is ignored.
- `ADMIN_EMAIL` and `ADMIN_PASSWORD` — your Super Admin login (password 12+ characters).

Everything else works as written. Use passwords made of letters, digits, `-`, `_`, `.` only:
the scripts place them into connection URLs as-is.

Already have a `backend/.env`? You do not need to replace it: see
[Existing `.env`](#existing-env).

## 2. Create the databases, roles, schema and demo data

```bash
cd backend
make dev/setup
```

This is idempotent; re-run it any time. It:

1. creates the Postgres roles `willcoll_app`, `willcoll_admin`, `willcoll_platform_app`,
   `willcoll_platform_admin` if they are missing (an existing role keeps its password);
2. creates the databases `willcoll_rentals` and `willcoll_platform` if they are missing;
3. runs every migration for both (`migrations/platform`, `migrations/rentals`);
4. seeds the demo firm once and the `TEST - KES 10` plan (see below) and the Super Admin;
5. prints the logins.

Demo data (`cmd/seed`): the firm *Kimani & Associates*, two landlords, two properties
(THE RUNDA'S ARCADE, KIWI PLACE) with units and leases, three months of billing history,
payments (some left in the review queue), an active Growth subscription, the Rentals
module with its trial, and its `module_usage` row.

**Logins**

| Who | Where | Email | Password |
|---|---|---|---|
| Manager | <http://localhost:3001/auth/login> (then Open Rentals) | `demo@willcoll.test` | `Demo-Password-1` |
| Super Admin | <http://localhost:3001/admin/login> | your `ADMIN_EMAIL` | your `ADMIN_PASSWORD` |

## 3. Start the backend

```bash
cd backend
make dev/up
```

It builds both APIs, starts them plus the fakes, waits for their health checks, then follows
their logs. **Ctrl+C stops everything.** Logs are also in `backend/bin/dev-logs/` (the previous run is kept as `*.log.prev`).

With no SMTP and fake SMS (the defaults), the things a real user would receive land in the logs:

- activation / password-reset / staff-invite links: `platform-api.log` (they point at the platform app, `FRONTEND_URL` = <http://localhost:3001> for platform-api; set `PLATFORM_FRONTEND_URL` in `.env` to change it)
- pay-page SMS codes: `rentals-api.log` (also at <http://127.0.0.1:14901/_sent>)

### The fake PayHero completes payments

After an STK push the fake PayHero waits ~3 seconds, then POSTs a realistic callback (same
payload shape, same `?token=` secret) to the push's `callback_url`, so the pay page reaches
"Confirmed". It logs one line, e.g. `[payhero] STK 18000 to +254722111001 -> success, callback 200`.
The last digit of the amount picks the outcome:

| Amount ends in | Outcome |
|---|---|
| `0`, `4`-`9` | success (receipt `FAKE...`) |
| `1` | cancelled by the user (ResultCode 1032) |
| `2` | insufficient funds (ResultCode 1) |
| `3` | no callback at all (a timeout: the pay page must expire on its own) |

To answer a push yourself, use the CLI. It targets the latest unanswered push, or `id=<external reference>`
(the automatic callback still fires for every amount except those ending in `3`):

```bash
make dev/payhero result=success            # or cancelled | insufficient | failed
make dev/payhero result=failed id=<ref>
```

Pay a rent of e.g. 18,001 for the cancelled path, 18,003 and then `make dev/payhero result=success`
to complete a "no callback" push by hand. The rehearsal fakes (`make rehearse/cutover`) do NOT
auto-complete; the smoke test drives the same `/_complete` endpoint itself.

### System health

The Super Admin page (platform app, `/admin/system`) is served by platform-api (`GET /v1/admin/system/health`): it
reads `willcoll_platform` itself and asks rentals-api for its own report (`RENTALS_API_URL`, set for you
by `make dev/up`). Each check has a short timeout (`HEALTH_TIMEOUT`, default 5s), so stopping rentals-api
shows Rentals as unreachable while the platform section still loads.

Run just one API (needs `make dev/setup` first): `make run/platform-api` or `make run/rentals-api`.

## 4. Start the frontend

In a second Git Bash window:

```bash
cd frontend
pnpm install      # first time only
pnpm dev
```

Open <http://localhost:3001> and sign in as the demo manager, then click *Open Rentals* (or
just open <http://localhost:3000>: you are sent to sign in and back).

`pnpm dev` runs `frontend/scripts/dev.mjs`, which reads the API ports from `backend/.env`
(`PORT`/`RENTALS_PORT`, `PLATFORM_PORT`) and starts **both** apps, platform on 3001 and
Rentals on 3000, with their proxies pointed at them. If either app exits, the other is
stopped. Run one alone with `pnpm --filter platform dev` or `pnpm --filter rentals dev`
(set `NUXT_API_PLATFORM_INTERNAL_BASE`, and for Rentals `NUXT_API_INTERNAL_BASE`, yourself).

`pnpm lint`, `pnpm typecheck`, `pnpm test` and `pnpm build` run across `layers/base`,
`platform` and `rentals`.

Tenant pay page: <http://localhost:3000/pay/runda-arcade/G1>, phone `0722111001`
(`0733222001` for KIWI PLACE `A1`); read the SMS code from `rentals-api.log`.

## Reset / clean up

| Want | Do |
|---|---|
| Replace the demo firm (also repairs a half-seeded one) | `DEV_RESEED=yes make dev/setup` |
| Start from empty databases | `psql "<your MIGRATE_DSN>" -c "DROP DATABASE willcoll_rentals WITH (FORCE)"`, same for `willcoll_platform`, then `make dev/setup` |
| Use real PayHero / Africa's Talking / SMTP | `DEV_FAKES=false` in `.env`, then fill the `PAYHERO_*`, `AFRICASTALKING_*`, `SMTP_*` values |
| Other database names | `DEV_RENTALS_DB=` / `DEV_PLATFORM_DB=` in `.env` (or on the command line) |
| Trial / plan helpers | `make dev/expire-trial EMAIL=...`, `dev/reset-trial`, `dev/set-plan` (they use `PLATFORM_DSN`; see the Makefile) |

## Existing `.env`

The dev stack **never uses `DB_DSN` as written in `.env`** and never connects to the database it
names (normally your existing `willcoll`). It derives its own connection strings for
`willcoll_rentals` / `willcoll_platform` from `MIGRATE_DSN` plus the role passwords, so your
existing data, `make test/integration` and anything else using `DB_DSN` are unaffected. If
`DEV_RENTALS_DB` / `DEV_PLATFORM_DB` ever equal that existing database (or `willcoll`), the
scripts refuse unless `CONFIRM_EXISTING_DB=yes` (migration 000045 drops its identity tables).

With the variables you already have (`MIGRATE_DSN`, `DB_DSN`, `DB_ADMIN_PASSWORD`, `PORT`,
`ADMIN_*`, ...) nothing has to be added. These lines are worth adding:

```
PLATFORM_PORT=4100
DEV_FAKES=true
DEV_RENTALS_DB=willcoll_rentals
DEV_PLATFORM_DB=willcoll_platform
```

(They are the defaults; writing them down makes the setup visible.) With `DEV_FAKES=true`,
`make dev/up` points both APIs at the local SMS/PayHero stand-ins and blanks `SMTP_HOST` **for those
processes only**, whatever your `.env` says, so nothing real is called or sent.

## Local staging (Docker)

The whole platform as containers, the way production will run it: Postgres (4 databases),
one-shot migrations, platform-api, rentals-api, both Nuxt apps, Caddy, the fake SMS/PayHero
service and Mailpit. It is separate from the plain-Go dev stack above and runs beside it
(different ports). Needs Docker Desktop running.

| What | URL / port |
|---|---|
| Platform app (sign in, account, Super Admin) | <https://app.willcoll.localhost:8443> |
| Rentals app and the public pay page | <https://rentals.willcoll.localhost:8443> |
| Mailpit inbox (activation / reset emails) | <http://127.0.0.1:8025> |
| Postgres, for psql / a GUI (superuser `postgres`) | `127.0.0.1:5433` (never 5432) |

**Commands (from `backend/`):**

| Want | Do |
|---|---|
| Start everything (builds, migrates, waits until healthy) | `make staging/up` |
| Demo firm + `TEST - KES 10` plan + Super Admin | `make staging/seed` (`RESEED=yes` replaces an existing demo firm) |
| Prove RLS applies to the API roles | `make staging/check-rls` |
| Logs / status | `make staging/logs` (`SVC=rentals-api` for one), `make staging/ps` |
| Stop (keeps data) | `make staging/down` |
| Wipe databases **and** Caddy's CA | `make staging/reset CONFIRM=yes` |

The first `staging/up` copies `deploy/local/.env.example` to `deploy/local/.env` (git-ignored;
throwaway local passwords). That is the only env file the stack reads: `backend/.env` is never
passed to a container, and nothing is bind-mounted (init scripts, the Caddyfile and migrations are
copied into their images). `DEV_FAKES=true`: SMS and PayHero go to local stand-ins and email goes
to Mailpit, so nothing real is ever called or sent. Demo logins are the same as the dev stack
(`demo@willcoll.test` / `Demo-Password-1`); the Super Admin is `ADMIN_EMAIL` / `ADMIN_PASSWORD`
from `deploy/local/.env` at `/admin/login`. Read the pay page's SMS code with
`make staging/logs SVC=fakes`.

### Trust Caddy's root certificate once (Windows)

Caddy signs the `*.willcoll.localhost` certificates with its own CA (`tls internal`). Until your
machine trusts that CA, the browser stops on a certificate warning and **stores no cookie**.
Chrome and Edge resolve `*.willcoll.localhost` to your own machine by themselves: no hosts-file
edit. The session cookie is `Secure; HttpOnly; SameSite=Lax; Domain=.willcoll.localhost`, which
both browsers accept and send to both hosts (verified in Chrome 154 and Edge 154).

1. `make staging/up`, then export the root certificate out of Caddy's data volume
   (`/data/caddy/pki/authorities/local/root.crt`):

   ```
   make staging/export-ca          # writes deploy/local/caddy-root.crt (git-ignored)
   ```

   (by hand: `docker compose -f ../deploy/local/docker-compose.yml cp caddy:/data/caddy/pki/authorities/local/root.crt caddy-root.crt`)

2. Trust it for your Windows user (no admin rights; Windows may ask you to confirm):

   ```
   certutil -user -addstore Root deploy\local\caddy-root.crt
   ```

   Restart Chrome/Edge fully afterwards. Firefox keeps its own store (Settings, Privacy, View
   Certificates, Authorities, Import).

3. To undo it: `certutil -user -delstore Root "Caddy Local Authority - 2026 ECC Root"`.

The CA lives in the `caddy_data` volume, so it survives `staging/down` and `staging/up`. After
`staging/reset` (which deletes the volume) Caddy makes a new one: delete the old entry (step 3),
then export and trust again. The CA's private key stays in that volume on your machine; the
certificate you trust is only good for `localhost` names Caddy issues here, but treat it like any
local dev CA and remove it when you stop using the stack.

### Things that are deliberately different from the dev stack

- **Database roles** (`deploy/postgres/init.sql`, reused in production): per database a migration
  *owner* and an API *app* role; rentals and platform also have an *admin* role (BYPASSRLS). The
  owner is BYPASSRLS because tenant tables have FORCE ROW LEVEL SECURITY and a data-fixing
  migration (000015) cannot run otherwise; owners are used by `migrate` and the seeder only.
  `make staging/check-rls` proves the API roles are not superuser/BYPASSRLS/owner, cannot alter
  the schema, and see zero rows for a tenant that owns nothing; it fails if that is ever wrong.
- **Web images take their API targets at run time** (`NUXT_API_INTERNAL_BASE`,
  `NUXT_API_PLATFORM_INTERNAL_BASE`, `NUXT_PUBLIC_APPS_*`): the same image runs in every
  environment.
- **Client IP.** Caddy replaces any `X-Forwarded-For` it is sent, the Nuxt servers append the peer
  they see, and the APIs believe the header only when their direct peer is in `TRUSTED_PROXIES`
  (the compose network's private ranges here; empty in plain `make dev`). In Docker Desktop the
  browser reaches Caddy through Docker's NAT, so request logs show `172.18.0.1` for you; a real
  server shows the real address.
- The Super Admin System page shows Backups as "not configured" locally; that is expected.

## Race detector: `make test/race`

`go test -race` needs cgo, and a bare Windows machine has no C compiler, so plain `make test`
needs `RACE=` here and runs without the detector. **`make test/race` (from `backend/`) is the
only way to get `-race` on this machine.** It needs Docker Desktop running and does this:

- runs `go test -race -vet=off ./...` inside `golang:<version in go.mod>` (the version is read
  from `go.mod`, so it follows the project);
- streams the source in with `tar` instead of bind-mounting it, leaving out `backend/.env` and
  `bin/`, and passes no environment variables, so the container can never see your real DSNs
  or passwords;
- keeps the Go module and build caches in the named volumes `willcoll-gomod` and
  `willcoll-gobuild` (the first run downloads modules; later runs are quick).

Database integration tests are skipped here (no Postgres in the container); run
`make test/integration` for those. CI runs the same race tests natively on Linux.

A stray `backend/.env` can also override DSNs when you run other `make` targets; `make
NO_DOTENV=1 <target>` ignores it. Images and containers never read it: every `.dockerignore`
excludes `.env*`, and Compose files must name their own env file (`deploy/local/.env`) with
`env_file`.

## Troubleshooting

- **`cannot connect with MIGRATE_DSN`**: Postgres is not running, or the user/password is wrong.
- **`role ... exists but the password dev knows for it does not work`**: the script prints the
  exact `ALTER ROLE` to run and which `.env` variable to set.
- **`something is already listening on port ...`**: an earlier `make dev/up` is still running
  (close its window) or another program holds the port.
- **Port 3000 or 3001 busy / `Another Nuxt dev server is already running`**: stop that one first
  (`pnpm dev -- --takeover` stops it and starts this one).
- **Signed in on one app but the other asks you to sign in again**: both must be opened as
  `localhost` (not `127.0.0.1` for one and `localhost` for the other): cookies are per host.
- **Seeding stopped part-way**: fix the error shown, then `DEV_RESEED=yes make dev/setup`.
