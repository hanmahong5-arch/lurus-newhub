#!/usr/bin/env bash
# One command that proves the private-inference claim end to end, using the
# FIRST-CLASS private-endpoint channel type (57) rather than the generic Custom
# escape hatch. Sibling of run-demo.sh; additive, does not modify it.
#
#   bash demo/private-inference/verify-private-endpoint-type.sh
#   bash demo/private-inference/verify-private-endpoint-type.sh stop
#
# What it asserts (each step FAILS the script if it does not hold):
#
#   1. FORWARD   — a tenant relay call for the intranet model returns 200 and
#                  the mock on-prem endpoint's hit counter increments. The
#                  prompt provably arrived at a loopback-bound server.
#   2. NEGATIVE  — a tenant relay call for the egress-canary model (a type-57
#                  channel whose base_url is a PUBLIC provider, written straight
#                  into the DB so it bypasses HTTP config validation) is
#                  REFUSED, the mock hit counter does NOT move, and the error
#                  says no request was sent.
#   3. NO EGRESS — the backend process holds zero non-loopback outbound TCP
#                  connections across the whole run.
#   4. CONSOLE   — GET /api/v2/<tenant>/private-routing reports verdict
#                  `all_traffic_stays_on_prem` and flags the canary as
#                  `will_be_blocked`, from the same classifier the guard uses.
#
# Prereqs: Docker Desktop (for the demo Postgres), Go toolchain, Bun (mock
# endpoint). Node only for the optional console screenshot.
set -uo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEMO_DIR/../.." && pwd)"
cd "$REPO_ROOT"

PG_CONTAINER=newhub-pgdemo
PG_PORT=5435
BACKEND_PORT=8099
MOCK_PORT=11434
TENANT=privacy-strict
TOKEN=privacystrict000000000000000000000000onprem02
INTRANET_MODEL=qwen2.5-7b-instruct-strict
CANARY_MODEL=egress-canary-model
VIEWER_USER_ID=9201
LOG_DIR="$REPO_ROOT/logs"
BACKEND_LOG="$LOG_DIR/backend-privendpoint.log"
MOCK_LOG="$LOG_DIR/mock-privendpoint.log"

step() { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }
ok()   { printf '   \033[32mPASS\033[0m %s\n' "$*"; }
die()  { printf '\n\033[1;31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

# Windows/MSYS2: netstat -ano is the reliable way to find a listener's PID.
pids_on_port() {
  netstat -ano 2>/dev/null | grep 'LISTENING' | grep ":$1 " | awk '{print $NF}' | sort -u
}
kill_port() {
  for pid in $(pids_on_port "$1"); do
    taskkill //F //PID "$pid" >/dev/null 2>&1 || kill -9 "$pid" >/dev/null 2>&1 || true
  done
}
mock_hits() { grep -c 'PRIVATE-ENDPOINT HIT' "$MOCK_LOG" 2>/dev/null || echo 0; }

if [ "${1:-}" = "stop" ]; then
  step "stopping backend :$BACKEND_PORT and mock :$MOCK_PORT (Postgres container left running)"
  kill_port "$BACKEND_PORT"
  kill_port "$MOCK_PORT"
  ok "stopped"
  exit 0
fi

mkdir -p "$LOG_DIR"

# --- 1) demo Postgres -------------------------------------------------------
step "1/6 demo Postgres (docker $PG_CONTAINER :$PG_PORT)"
if ! docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
  if docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
    docker start "$PG_CONTAINER" >/dev/null || die "docker start $PG_CONTAINER failed"
    # Docker Desktop quirk: a long-stopped container can start with its host
    # port-forward proxy unbound. A full restart fixes it (see README).
    if ! docker inspect "$PG_CONTAINER" --format '{{json .NetworkSettings.Ports}}' | grep -q "$PG_PORT"; then
      echo "   host port $PG_PORT not bound after start — docker restart..."
      docker restart "$PG_CONTAINER" >/dev/null || die "docker restart failed"
    fi
  else
    docker run -d --name "$PG_CONTAINER" -p "$PG_PORT:5432" \
      -e POSTGRES_PASSWORD=postgres -e POSTGRES_USER=postgres -e POSTGRES_DB=newhub \
      postgres:16 >/dev/null || die "docker run postgres failed"
  fi
fi
for _ in $(seq 1 60); do
  docker exec "$PG_CONTAINER" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$PG_CONTAINER" pg_isready -U postgres >/dev/null 2>&1 || die "Postgres not ready"
ok "Postgres ready on :$PG_PORT"

# --- 2) backend -------------------------------------------------------------
step "2/6 newhub backend (go run ./cmd/server :$BACKEND_PORT) — restarted so the build under test is the one serving"
kill_port "$BACKEND_PORT"
sleep 1
set -a; . "$DEMO_DIR/.env.demo"; set +a
nohup go run ./cmd/server > "$BACKEND_LOG" 2>&1 &
boot_ok=false
for i in $(seq 1 120); do
  if [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://localhost:$BACKEND_PORT/api/status" 2>/dev/null)" = "200" ]; then
    boot_ok=true; ok "backend up after ${i}s"; break
  fi
  sleep 1
done
$boot_ok || { tail -n 30 "$BACKEND_LOG"; die "backend did not boot; see $BACKEND_LOG"; }

# --- 3) seed (idempotent, pure config) --------------------------------------
step "3/6 seed the tenant + type-57 channels (pure config, ON CONFLICT idempotent)"
docker exec -i "$PG_CONTAINER" psql -U postgres -d newhub -q -f - < "$DEMO_DIR/seed-private-endpoint-type.sql" >/dev/null \
  || die "seed-private-endpoint-type.sql failed"
docker exec -i "$PG_CONTAINER" psql -U postgres -d newhub -q -f - < "$DEMO_DIR/seed-strict-console-viewer.sql" >/dev/null \
  || die "seed-strict-console-viewer.sql failed"
seeded=$(docker exec "$PG_CONTAINER" psql -U postgres -d newhub -tAc \
  "SELECT count(*) FROM channels WHERE tenant_id='$TENANT' AND type=57")
[ "$(echo "$seeded" | tr -d '[:space:]')" = "2" ] || die "expected 2 type-57 channels, got $seeded"
ok "2 type-57 channels seeded (1 intranet + 1 egress canary)"

# --- 4) mock private endpoint ----------------------------------------------
step "4/6 mock on-prem inference endpoint (bun, loopback-only :$MOCK_PORT)"
kill_port "$MOCK_PORT"
sleep 1
: > "$MOCK_LOG"
nohup bun "$DEMO_DIR/mock-openai-endpoint.mjs" > "$MOCK_LOG" 2>&1 &
mock_ok=false
for i in $(seq 1 30); do
  curl -s -m 2 "http://127.0.0.1:$MOCK_PORT/v1/models" >/dev/null 2>&1 && { mock_ok=true; ok "mock bound :$MOCK_PORT after ${i}s"; break; }
  sleep 1
done
$mock_ok || { tail -n 20 "$MOCK_LOG"; die "mock endpoint did not bind :$MOCK_PORT"; }

BACKEND_PID="$(pids_on_port "$BACKEND_PORT" | head -1)"
[ -n "$BACKEND_PID" ] || die "could not resolve backend PID on :$BACKEND_PORT"
echo "   backend PID = $BACKEND_PID"

outbound_public() {
  netstat -ano 2>/dev/null | awk -v p="$BACKEND_PID" \
    '$NF==p && $1=="TCP" && $4!="LISTENING" && $3 !~ /^127\./ && $3 !~ /^\[::1\]/ {print $0}'
}

# --- 5) the two relay calls ------------------------------------------------
step "5/6 FORWARD — tenant relay call for the intranet model"
hits_before=$(mock_hits)
fwd_body=$(curl -s -m 30 -w $'\n%{http_code}' "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$INTRANET_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Summarize our internal Q3 revenue memo.\"}]}")
fwd_code=$(printf '%s' "$fwd_body" | tail -n1)
[ "$fwd_code" = "200" ] || { echo "$fwd_body"; die "forward call expected 200, got $fwd_code"; }
hits_after=$(mock_hits)
[ "$hits_after" -gt "$hits_before" ] || die "mock endpoint was NOT hit (before=$hits_before after=$hits_after) — routing did not reach the private endpoint"
ok "relay returned 200 AND the loopback-bound endpoint logged the hit"
echo "   ---- mock endpoint log (the proof the prompt landed on-prem) ----"
sed -n "/PRIVATE-ENDPOINT HIT #$hits_after/,/^=\{10,\}$/p" "$MOCK_LOG" | sed 's/^/   /'

step "5b/6 NEGATIVE — same tenant, egress-canary model (public base_url, DB-inserted to bypass config validation)"
hits_before_canary=$(mock_hits)
can_body=$(curl -s -m 30 -w $'\n%{http_code}' "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$CANARY_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"This must never reach a SaaS provider.\"}]}")
can_code=$(printf '%s' "$can_body" | tail -n1)
can_msg=$(printf '%s' "$can_body" | sed '$d')
[ "$can_code" = "200" ] && { echo "$can_msg"; die "CANARY WAS SERVED — the egress guard is broken, prompts can leave the network"; }
printf '%s' "$can_msg" | grep -q 'no request was sent' \
  || { echo "$can_msg"; die "canary was refused but not by the egress guard — check the failure reason"; }
[ "$(mock_hits)" = "$hits_before_canary" ] || die "canary somehow reached the mock endpoint"
ok "canary refused (HTTP $can_code) with 'no request was sent'; mock hit count unchanged"
echo "   ---- gateway refusal ----"
printf '   %s\n' "$can_msg"

step "5c/6 NO EGRESS — backend's non-loopback outbound TCP connections"
leaks=$(outbound_public)
if [ -n "$leaks" ]; then
  echo "$leaks" | sed 's/^/   /'
  die "backend holds non-loopback outbound connections — investigate before claiming zero egress"
fi
ok "zero non-loopback outbound connections for PID $BACKEND_PID"

# --- 6) console status ------------------------------------------------------
step "6/6 CONSOLE — GET /api/v2/$TENANT/private-routing (admin-gated, bridge session)"
BRIDGE_TOKEN="$(grep '^E2E_BRIDGE_TOKEN=' "$DEMO_DIR/.env.demo" | cut -d= -f2-)"
cookie=$(curl -s -i -X POST "http://localhost:$BACKEND_PORT/api/v2/bridge/exchange?token=$BRIDGE_TOKEN&user_id=$VIEWER_USER_ID" \
  | grep -i '^set-cookie:' | sed 's/^[Ss]et-[Cc]ookie: //' | cut -d';' -f1 | head -1)
[ -n "$cookie" ] || die "bridge login did not return a session cookie"
status_json=$(curl -s -m 20 -H "Cookie: $cookie" "http://localhost:$BACKEND_PORT/api/v2/$TENANT/private-routing")
printf '%s' "$status_json" | grep -q '"verdict":"all_traffic_stays_on_prem"' \
  || { echo "$status_json"; die "console verdict is not all_traffic_stays_on_prem"; }
printf '%s' "$status_json" | grep -q '"will_be_blocked":true' \
  || { echo "$status_json"; die "console does not flag the egress canary as blocked"; }
ok "verdict=all_traffic_stays_on_prem and the canary is flagged will_be_blocked"

if command -v node >/dev/null 2>&1 && [ -d "$REPO_ROOT/web/node_modules/playwright-core" ]; then
  shot="$REPO_ROOT/_bmad-output/private-routing-strict-console.png"
  BACKEND_URL="http://localhost:$BACKEND_PORT" E2E_BRIDGE_TOKEN="$BRIDGE_TOKEN" \
  BRIDGE_USER_ID="$VIEWER_USER_ID" TENANT_SLUG="$TENANT" SHOT_PATH="$shot" \
    node "$DEMO_DIR/shot-private-routing.mjs" >/dev/null 2>&1 \
    && ok "console screenshot → $shot" \
    || echo "   (screenshot skipped — see README's Playwright notes; run it with node, not bun)"
else
  echo "   (screenshot skipped — node and/or web/node_modules/playwright-core not present)"
fi

printf '\n\033[1;32mALL CHECKS PASSED\033[0m — tenant %s routes to its own intranet endpoint; the public-address canary is refused before any connection opens.\n' "$TENANT"
printf 'Stop the backend + mock with: bash demo/private-inference/verify-private-endpoint-type.sh stop\n'
