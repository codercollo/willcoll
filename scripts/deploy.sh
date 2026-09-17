#!/usr/bin/env bash
# Runs ON the Contabo VPS, invoked over SSH by .github/workflows/deploy.yml
# on every push to main. git pull -> build -> migrate (one-shot, always
# before the new api container serves traffic) -> restart (Phase 7.2).
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE="docker compose --env-file .env -f build/docker-compose.yml -f build/docker-compose.prod.yml"

git pull
$COMPOSE build postgres api web ml-sidecar caddy pg_backup
$COMPOSE run --rm migrate
$COMPOSE up -d --remove-orphans

echo "deploy.sh: done"
