.PHONY: up down logs migrate sqlc seed test up-prod deploy backup-now restore reset-dev

# Compose files live under build/ (spec §7). docker-compose.yml is dev-ready;
# docker-compose.prod.yml layers on top for the Contabo VPS.
# --env-file .env is required: with -f build/..., Compose otherwise looks
# for .env in build/ (the first -f file's directory), not the repo root, and
# silently substitutes blank values instead of failing loud.
COMPOSE_DEV  := --env-file .env -f build/docker-compose.yml
COMPOSE_PROD := --env-file .env -f build/docker-compose.yml -f build/docker-compose.prod.yml

up:
	docker compose $(COMPOSE_DEV) up --build -d

down:
	docker compose $(COMPOSE_DEV) down

# Postgres only applies POSTGRES_USER/PASSWORD/DB on first init of an empty
# volume — editing .env afterwards does nothing until the volume is dropped.
# Use this whenever you hit "password authentication failed" after changing
# .env, instead of hand-debugging a stale pg_data volume.
reset-dev:
	docker compose $(COMPOSE_DEV) down -v
	docker compose $(COMPOSE_DEV) up --build -d --wait
	# On a first-ever init, Postgres restarts itself right after the
	# healthcheck first goes green (initdb -> internal restart) — one quick
	# retry absorbs that window instead of failing make on a false start.
	docker compose $(COMPOSE_DEV) --profile tools run --rm migrate \
		|| (sleep 2 && docker compose $(COMPOSE_DEV) --profile tools run --rm migrate)
	docker compose $(COMPOSE_DEV) --profile tools run --rm seed

logs:
	docker compose $(COMPOSE_DEV) logs -f

# Both run inside the migrate/seed containers against THIS compose's own
# postgres service — never a bare `go run` against whatever DATABASE_DSN
# happens to be in the host's shell (spec Phase 8.1).
migrate:
	docker compose $(COMPOSE_DEV) --profile tools run --rm migrate

sqlc:
	sqlc generate

seed:
	docker compose $(COMPOSE_DEV) --profile tools run --rm seed

test:
	go test ./...

up-prod:
	docker compose $(COMPOSE_PROD) up --build -d

deploy:
	bash scripts/deploy.sh

backup-now:
	docker compose $(COMPOSE_PROD) exec -T pg_backup /scripts/nightly-pg-dump.sh

# Restores a nightly-pg-dump.sh gzip dump into the PROD postgres service.
# Destructive — requires FILE= explicitly, no default, no glob-guessing.
restore:
	@test -n "$(FILE)" || { echo "usage: make restore FILE=/path/to/willcoll-*.sql.gz"; exit 1; }
	@test -f "$(FILE)" || { echo "no such file: $(FILE)"; exit 1; }
	gunzip -c "$(FILE)" | docker compose $(COMPOSE_PROD) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'
