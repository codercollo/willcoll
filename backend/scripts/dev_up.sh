#!/usr/bin/env sh
# Starts the whole local backend: platform-api, rentals-api and (unless
# DEV_FAKES=false) local stand-ins for Africa's Talking and PayHero. Ctrl+C
# stops everything. Logs go to bin/dev-logs/ and are followed here: the pay-page
# SMS codes (also in fakes.log) and the activation/reset links (no SMTP in dev)
# are written to them.
#
#   make dev/up        (run `make dev/setup` once first)
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib_dev.sh

dev_init

for db in "$RENTALS_DB" "$PLATFORM_DB"; do
    [ "$(psql "$MIGRATE_DSN" -Atc "SELECT 1 FROM pg_database WHERE datname = '$db'")" = 1 ] ||
        dev_die "database $db does not exist: run \`make dev/setup\` first"
done
for p in "$PLATFORM_PORT" "$RENTALS_PORT"; do
    if curl -s -o /dev/null "http://127.0.0.1:$p/v1/healthcheck"; then
        dev_die "something is already listening on port $p (an earlier dev/up still running?)"
    fi
done

BIN=./bin
LOGS=$BIN/dev-logs
mkdir -p "$LOGS"
PIDS=""
stop_all() {
    for pid in $PIDS; do kill "$pid" 2>/dev/null || true; done
    PIDS=""
}
trap 'stop_all; echo; echo "stopped"; exit 0' INT TERM
trap stop_all EXIT

echo "building..."
go build -o "$BIN/dev-platform-api" ./cmd/platform-api
go build -o "$BIN/dev-rentals-api" ./cmd/rentals-api
[ "$DEV_FAKES" = false ] || go build -o "$BIN/dev-fakes" ./cmd/rehearse
# Keep the previous run's logs (<name>.log.prev) instead of wiping them: the
# evidence for "what happened at 11:24" is gone the moment dev/up restarts.
for f in platform-api rentals-api fakes; do
    [ ! -s "$LOGS/$f.log" ] || cp "$LOGS/$f.log" "$LOGS/$f.log.prev"
done
: >"$LOGS/platform-api.log"
: >"$LOGS/rentals-api.log"
: >"$LOGS/fakes.log"

if [ "$DEV_FAKES" != false ]; then
    FAKE_SMS_ADDR=$FAKE_SMS FAKE_PAYHERO_ADDR=$FAKE_PAYHERO FAKE_PAYHERO_AUTOCALLBACK=true "$BIN/dev-fakes" fakes >"$LOGS/fakes.log" 2>&1 &
    PIDS="$PIDS $!"
fi
(dev_service_env platform && exec "$BIN/dev-platform-api") >"$LOGS/platform-api.log" 2>&1 &
PIDS="$PIDS $!"
(dev_service_env rentals && exec "$BIN/dev-rentals-api") >"$LOGS/rentals-api.log" 2>&1 &
PIDS="$PIDS $!"

wait_for() { # url name log
    i=0
    while [ $i -lt 80 ]; do
        if curl -fsS "$1" >/dev/null 2>&1; then echo "  $2 is up"; return 0; fi
        i=$((i + 1))
        sleep 0.5
    done
    echo "$2 did not come up at $1; last of its log:" >&2
    tail -n 20 "$3" >&2
    return 1
}
wait_for "http://127.0.0.1:$PLATFORM_PORT/v1/healthcheck" "platform-api :$PLATFORM_PORT" "$LOGS/platform-api.log"
wait_for "http://127.0.0.1:$RENTALS_PORT/v1/healthcheck" "rentals-api  :$RENTALS_PORT" "$LOGS/rentals-api.log"

cat <<MSG

Backend is up. Now, in another terminal:   cd frontend && pnpm dev
then open http://localhost:3001   (platform: sign in; Rentals is :3000. Ctrl+C here stops the backend)
Following the logs (SMS codes and email links appear here):

MSG
# pretty: SMS (fakes.log), OTP and email lines become one readable line each;
# everything else passes through unchanged.
pretty() {
    while IFS= read -r line; do
        case $line in
        "==>"* | "") ;;
        "SMS to "*) echo "[sms] ${line#SMS to }" ;;
        *'"msg":"OTP for '*)
            echo "$line" | sed -n 's/.*"msg":"\(OTP for [^"]*\)".*/[otp] \1/p'
            ;;
        *'"msg":"email (not sent'*)
            to=$(echo "$line" | sed -n 's/.*"to":"\([^"]*\)".*/\1/p')
            subject=$(echo "$line" | sed -n 's/.*"subject":"\([^"]*\)".*/\1/p')
            link=$(echo "$line" | sed 's/\\u0026/\&/g' | grep -o 'https\{0,1\}://[^ \\"]*' | head -n 1)
            echo "[email] to $to | $subject | ${link:-no link}"
            ;;
        *) echo "$line" ;;
        esac
    done
}
tail -n 0 -f "$LOGS/platform-api.log" "$LOGS/rentals-api.log" "$LOGS/fakes.log" 2>/dev/null | pretty &
PIDS="$PIDS $!"
wait
