#!/usr/bin/env bash
# Real-chain acceptance on a local full stack: PostgreSQL 16 + Redis + the
# real binary with its real migrations + the fake vendor and fake platform
# wallet (cmd/fakeupstream), then the executable acceptance scenarios
# (web/tests/acceptance) against it, then the meta-gate that refuses a hollow
# run.
#
#   bash scripts/acceptance-stack.sh            # build, start, run, tear down
#   ACCEPT_KEEP=1 bash scripts/acceptance-stack.sh   # leave the stack up after
#   ACCEPT_UP_ONLY=1 bash scripts/acceptance-stack.sh # start it, write
#       web/acceptance-report/stack.env, run nothing, leave it up
#
# Linux only (the self-hosted CI runner and WSL). Needs go, bun, curl, and
# docker unless both ACCEPT_SQL_DSN and ACCEPT_REDIS_URL are given (CI passes
# its service containers in). Every run gets fresh databases when docker is
# used; scenarios name what they create per run, so a reused database works
# too.
#
# The backend runs with r6-stage's environment (deploy/k8s/r6-stage/
# deployment.yaml) wherever that is possible on one machine. The deviations
# are deliberate and listed where they are made, below.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/newhub-accept.XXXXXX")"
REPORT_DIR="$REPO_ROOT/web/acceptance-report"
mkdir -p "$REPORT_DIR"
CONTAINERS=()
PIDS=()

log() { printf '[acceptance] %s\n' "$*"; }
die() { printf '[acceptance] FAIL: %s\n' "$*" >&2; exit 1; }

cleanup() {
  local rc=$?
  if [ "${ACCEPT_KEEP:-}" = 1 ] && [ $rc -eq 0 ]; then
    log "ACCEPT_KEEP=1: stack left running (pids ${PIDS[*]:-none}, containers ${CONTAINERS[*]:-none}, work $WORK)"
    return
  fi
  for pid in "${PIDS[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; done
  for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && docker rm -f "$c" >/dev/null 2>&1 || true; done
  # Logs are evidence: keep them next to the report before the work dir goes.
  cp "$WORK"/*.log "$REPORT_DIR"/ 2>/dev/null || true
  rm -rf "$WORK"
  exit $rc
}
trap cleanup EXIT

port_free() { ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
free_port() {
  local p
  for _ in $(seq 1 50); do
    p=$((20000 + RANDOM % 20000))
    port_free "$p" && { echo "$p"; return; }
  done
  die "no free port found"
}

wait_http() { # url seconds what logfile
  local url=$1 secs=$2 what=$3 logf=${4:-}
  for _ in $(seq 1 "$secs"); do
    curl -fsS -o /dev/null "$url" 2>/dev/null && return 0
    sleep 1
  done
  [ -n "$logf" ] && tail -n 60 "$logf" >&2
  die "$what did not answer $url within ${secs}s"
}

# Children get their own session so that a stack left up (ACCEPT_KEEP /
# ACCEPT_UP_ONLY) survives the shell that started it — under `wsl -- bash`
# the session's exit otherwise takes them down with SIGHUP.
DETACH=""
command -v setsid >/dev/null && DETACH=setsid

for cmd in go bun curl; do command -v "$cmd" >/dev/null || die "missing $cmd"; done

# --- 1. PostgreSQL 16 and Redis ---------------------------------------------
SQL_DSN="${ACCEPT_SQL_DSN:-}"
PG_CONTAINER=""
if [ -z "$SQL_DSN" ]; then
  command -v docker >/dev/null || die "ACCEPT_SQL_DSN unset and no docker to start PostgreSQL"
  pg_port=$(free_port)
  name="newhub-accept-pg-$$"
  docker run -d --rm --name "$name" --tmpfs /var/lib/postgresql/data \
    -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=newhub \
    -p "127.0.0.1:${pg_port}:5432" postgres:16-alpine >/dev/null
  CONTAINERS+=("$name")
  PG_CONTAINER="$name"
  for _ in $(seq 1 60); do
    docker exec "$name" pg_isready -U postgres -d newhub >/dev/null 2>&1 && break
    sleep 1
  done
  SQL_DSN="postgres://postgres:postgres@127.0.0.1:${pg_port}/newhub?sslmode=disable"
fi
REDIS_URL="${ACCEPT_REDIS_URL:-}"
if [ -z "$REDIS_URL" ]; then
  command -v docker >/dev/null || die "ACCEPT_REDIS_URL unset and no docker to start Redis"
  redis_port=$(free_port)
  name="newhub-accept-redis-$$"
  docker run -d --rm --name "$name" -p "127.0.0.1:${redis_port}:6379" redis:7-alpine >/dev/null
  CONTAINERS+=("$name")
  # Production uses DB 2; so does this.
  REDIS_URL="redis://127.0.0.1:${redis_port}/2"
fi
log "postgres: ${SQL_DSN%%\?*}"
log "redis:    $REDIS_URL"

# --- 2. Build -----------------------------------------------------------------
# The SPA is embedded (//go:embed all:dist); the console steps need the real
# one, not CI's stub page.
if [ "${ACCEPT_SKIP_WEB_BUILD:-}" != 1 ] && ! ls web/dist/assets/*.js >/dev/null 2>&1; then
  log "building web/dist"
  (cd web && bun install --frozen-lockfile >/dev/null && bun run build >"$WORK/web-build.log" 2>&1) \
    || { tail -n 40 "$WORK/web-build.log" >&2; die "web build failed"; }
fi
[ -f web/dist/index.html ] || die "web/dist/index.html missing (set ACCEPT_SKIP_WEB_BUILD only when dist is already built)"
VERSION="$(cat VERSION 2>/dev/null || true)"
VERSION="${VERSION:-dev}-accept-$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
log "building lurus-api ($VERSION) and fakeupstream"
CGO_ENABLED=0 go build -ldflags "-X 'github.com/LurusTech/lurus-hub/internal/pkg/common.Version=${VERSION}'" \
  -o "$WORK/lurus-api" ./cmd/server
CGO_ENABLED=0 go build -o "$WORK/fakeupstream" ./cmd/fakeupstream

# --- 3. Fake vendor + fake platform -----------------------------------------
FAKE_PORT=$(free_port)
FAKE_URL="http://127.0.0.1:${FAKE_PORT}"
INTERNAL_KEY="accept-platform-key-$$"
VENDOR_KEY="accept-vendor-key-$$"
$DETACH "$WORK/fakeupstream" -addr "127.0.0.1:${FAKE_PORT}" -key "$VENDOR_KEY" -platform-key "$INTERNAL_KEY" \
  >"$WORK/fakeupstream.log" 2>&1 &
PIDS+=($!)
wait_http "$FAKE_URL/_fake/healthz" 20 fakeupstream "$WORK/fakeupstream.log"

# --- 4. The backend -------------------------------------------------------------
API_PORT=$(free_port)
BASE_URL="http://127.0.0.1:${API_PORT}"
BRIDGE_TOKEN="accept-bridge-$(date +%s)-$$"
(
  cd "$WORK"   # no .env from the repo may leak in: godotenv reads ./.env
  # r6-stage values:
  export PORT="$API_PORT" TZ=Asia/Shanghai GIN_MODE=release NODE_TYPE=master
  export SQL_DSN REDIS_CONN_STRING="$REDIS_URL"
  export SESSION_SECRET="accept-session-$$" SESSION_COOKIE_DOMAIN=""
  export ERROR_LOG_ENABLED=true MEILISEARCH_ENABLED=false
  export BILLING_UNIFIED_ENABLED=true CREDIT_POOL_REQUIRED=enforce
  export SYNC_FREQUENCY=60 SQL_MAX_OPEN_CONNS=12 SQL_MAX_IDLE_CONNS=12 RELAY_MAX_CONCURRENT_PER_TENANT=64
  export LOG_FORMAT=json
  # One run reads exports/statements ~17 times from one IP; the default of 20
  # per 20 minutes would fail an immediate rerun (UAT sets 200 too).
  export CRITICAL_RATE_LIMIT=200
  # The platform is the fake one, on loopback.
  export IDENTITY_SERVICE_URL="$FAKE_URL" IDENTITY_SERVICE_INTERNAL_KEY="$INTERNAL_KEY"
  # Deviations from r6-stage, each because one machine cannot have it:
  #  - no IdP: OIDC off, sessions come from the e2e bridge (as on UAT);
  #  - plain http on loopback: the session cookie cannot be Secure;
  #  - no NATS: quota events are not part of these scenarios.
  export OIDC_ENABLED=false E2E_BRIDGE_TOKEN="$BRIDGE_TOKEN" SESSION_SECURE=false
  export LLM_QUOTA_NATS_ENABLED=false
  exec $DETACH "$WORK/lurus-api" >"$WORK/lurus-api.log" 2>&1
) &
PIDS+=($!)
wait_http "$BASE_URL/api/status" 120 lurus-api "$WORK/lurus-api.log"

# Identity of what is under test, printed and written next to the report so a
# green run against the wrong instance is visible.
status_json="$(curl -fsS "$BASE_URL/api/status")"
reported_version="$(printf '%s' "$status_json" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
[ "$reported_version" = "$VERSION" ] || die "instance reports version '$reported_version', built '$VERSION' — testing the wrong process"
printf '{"target":"local","base_url":"%s","version":"%s","commit":"%s"}\n' \
  "$BASE_URL" "$reported_version" "$(git rev-parse HEAD 2>/dev/null || echo unknown)" >"$REPORT_DIR/target.json"
log "under test: $(cat "$REPORT_DIR/target.json")"

# --- 5. Scenarios + meta-gate -------------------------------------------------
cat >"$REPORT_DIR/stack.env" <<ENV
export ACCEPT_TARGET=local ACCEPT_BASE_URL=$BASE_URL ACCEPT_BRIDGE_TOKEN=$BRIDGE_TOKEN
export ACCEPT_FAKE_URL=$FAKE_URL ACCEPT_VENDOR_KEY=$VENDOR_KEY
export ACCEPT_PG_CONTAINER=$PG_CONTAINER ACCEPT_SQL_DSN='$SQL_DSN'
ENV
if [ "${ACCEPT_UP_ONLY:-}" = 1 ]; then
  ACCEPT_KEEP=1
  log "up: source web/acceptance-report/stack.env; logs in $WORK"
  exit 0
fi
cd web
[ -d node_modules ] || bun install --frozen-lockfile >/dev/null
export ACCEPT_TARGET=local ACCEPT_BASE_URL="$BASE_URL" ACCEPT_BRIDGE_TOKEN="$BRIDGE_TOKEN"
export ACCEPT_FAKE_URL="$FAKE_URL" ACCEPT_VENDOR_KEY="$VENDOR_KEY"
# TC-E* stamps the first customer admin's role directly (fixtures/enterprise.ts).
export ACCEPT_PG_CONTAINER="$PG_CONTAINER" ACCEPT_SQL_DSN="$SQL_DSN"
# A report left by an earlier run must not be read as this run's.
rm -f acceptance-report/results.json
set +e
bunx playwright test -c playwright.acceptance.config.ts "$@"
pw_rc=$?
set -e
bun scripts/acceptance-meta-gate.mjs acceptance-report/results.json "$FAKE_URL" || pw_rc=1
exit $pw_rc
