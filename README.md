# Willcoll

Rent-management SaaS. See `willcoll-spec.md` for the full spec and `project-structure.txt` for the repo layout.

## Dev setup

```
cp .env.example .env   # then fill in real values
make up                # starts the postgres container (build/docker-compose.yml)
make migrate
make seed
go run ./cmd/willcoll
```

### `DATABASE_DSN`: host vs. container

Postgres runs in Docker; the Go API runs on your host during normal dev (`go run ./cmd/willcoll`), not inside Compose. These two must point at Postgres differently, because `postgres` (the Compose service name) only resolves *inside* the Compose network:

| Where the Go process runs | `DATABASE_DSN` host |
|---|---|
| Host (`go run ./cmd/willcoll`, normal dev) | `localhost:5432` |
| Inside Compose (prod-style, future phase) | `postgres:5432` |

`.env`'s `DATABASE_DSN` is the host form. If you ever run the API itself inside Compose, override it there to the container form — don't change `.env`'s copy, or host dev breaks.

### "password authentication failed for user ..."

Postgres only applies `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` on the **first init of an empty volume**. Editing `.env` after the volume already exists does nothing — the container keeps whatever credentials it was first created with. This is the #1 cause of local auth failures after pulling changes or editing `.env`.

Fix:

```
make reset-dev
```

This drops the Postgres volume, recreates it (applying your current `.env`), and re-runs migrate + seed. Check `docker compose -f build/docker-compose.yml ps` first if you want to confirm the volume/container state before nuking it.

### Port 5432 already in use

Postgres publishes on host port **5433**, not 5432 — a native Postgres install on the machine may already own 5432, and connections would silently land on that instead of the container (different credentials, looks identical to a password mismatch). If you see `docker compose ... up` warn about orphan `api`/`web` containers, that's leftover from an earlier, different compose file shape — safe to ignore, or clean up with `docker compose -f build/docker-compose.yml up --remove-orphans`.
