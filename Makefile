.PHONY: up down logs migrate sqlc seed test up-prod deploy backup-now restore

# Compose files live under build/ (spec §7). docker-compose.yml is dev-ready;
# docker-compose.prod.yml layers on top for the Contabo VPS.
COMPOSE_DEV  := -f build/docker-compose.yml
COMPOSE_PROD := -f build/docker-compose.yml -f build/docker-compose.prod.yml

up:
	docker compose $(COMPOSE_DEV) up --build -d

down:
	docker compose $(COMPOSE_DEV) down

logs:
	docker compose $(COMPOSE_DEV) logs -f

migrate:
	go run ./cmd/migrate

sqlc:
	sqlc generate

seed:
	go run ./cmd/willcoll-seed

test:
	go test ./...

up-prod:
	docker compose $(COMPOSE_PROD) up --build -d

deploy:
	bash scripts/deploy.sh

backup-now:
	docker compose $(COMPOSE_PROD) exec -T pg_backup scripts/backup/nightly-pg-dump.sh

restore:
	@echo "TODO: pg_restore $(FILE) into postgres (phase 11)"
