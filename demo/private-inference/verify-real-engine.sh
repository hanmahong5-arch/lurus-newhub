#!/usr/bin/env bash
# Prove the private-inference claim against a REAL inference server instead of
# the hand-written mock.
#
#   bash demo/private-inference/verify-real-engine.sh
#   bash demo/private-inference/verify-real-engine.sh stop
#
# Why a second script rather than an edit to verify-private-endpoint-type.sh
# --------------------------------------------------------------------------
# The two answer different questions and both are worth keeping:
#
#   verify-private-endpoint-type.sh   Does the GUARD work? Deterministic, no
#                                     weights on disk, safe to run in CI.
#   verify-real-engine.sh  (this)     Does the PRODUCT CLAIM hold? Needs a real
#                                     local model; not CI-able on a build agent.
#
# The mock's weakness is structural, not a bug: it was written by the same hand
# as the guard it exercises, so it can demonstrate plumbing but cannot evidence
# "a model answered, on this host, without egress". This script closes that gap
# by refusing to trust any endpoint it cannot first prove is a real model.
#
# What it asserts (every step FAILS the script if it does not hold):
#
#   1. ENGINE IS REAL   — the discovered endpoint answers two different prompts
#                         with two different, individually CORRECT answers, and
#                         reports prompt_tokens that track the input. A canned
#                         responder fails all three. This is also how the mock
#                         on :11434 is auto-rejected during discovery.
#   2. ENGINE IS LOCAL  — it listens on loopback only (no 0.0.0.0 / LAN bind)
#                         and holds zero non-loopback outbound connections, so
#                         it is serving weights from this disk, not proxying.
#   3. FORWARD          — a tenant relay call through the gateway returns 200
#                         and the answer is the model's, verifiably correct.
#   3b. STREAMING       — the same call with stream:true yields real SSE frames
#                         that reassemble into the same correct answer. SSE is
#                         what a chat UI actually uses, so it is the path the
#                         claim has to hold on.
#   4. NEGATIVE         — the egress canary (type-57 channel with a PUBLIC
#                         base_url, DB-inserted to bypass config validation) is
#                         still refused with no request emitted.
#   4b. STREAMING CANARY— asking for SSE does not make the canary reachable, and
#                         not one frame is emitted before the refusal. A guard
#                         that only fired after the stream opened would already
#                         have put the prompt on the wire.
#   5. NO EGRESS        — neither the gateway nor the engine holds a single
#                         non-loopback outbound TCP connection across the run.
#   6. CONSOLE          — GET /api/v2/<tenant>/private-routing still reports
#                         all_traffic_stays_on_prem with the real channel listed
#                         as intranet.
#
# Prereqs: Docker Desktop (demo Postgres), Go toolchain, and an OpenAI-compatible
# inference server already running on a loopback port of this machine, serving a
# chat model under the tenant-facing name in $ENGINE_MODEL. Override discovery
# with PRIVATE_ENGINE_URL / PRIVATE_ENGINE_MODEL.
set -uo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEMO_DIR/../.." && pwd)"
cd "$REPO_ROOT"

PG_CONTAINER=newhub-pgdemo
PG_PORT=5435
BACKEND_PORT=8099
TENANT=privacy-strict
TOKEN=privacystrict000000000000000000000000onprem02
CANARY_MODEL=egress-canary-model
VIEWER_USER_ID=9201
REAL_CHANNEL_NAME='Private Inference (strict · real local engine)'

ENGINE_MODEL="${PRIVATE_ENGINE_MODEL:-onprem-chat-8b}"
ENGINE_PORTS="${PRIVATE_ENGINE_PORTS:-11400 11434 8000 8080 1234 5000}"
CHAT_TIMEOUT="${PRIVATE_ENGINE_TIMEOUT:-300}"

LOG_DIR="$REPO_ROOT/logs"
BACKEND_LOG="$LOG_DIR/backend-realengine.log"

step() { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }
ok()   { printf '   \033[32mPASS\033[0m %s\n' "$*"; }
info() { printf '   %s\n' "$*"; }
die()  { printf '\n\033[1;31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

pids_on_port() {
  netstat -ano 2>/dev/null | grep 'LISTENING' | grep ":$1 " | awk '{print $NF}' | sort -u
}
kill_port() {
  for pid in $(pids_on_port "$1"); do
    taskkill //F //PID "$pid" >/dev/null 2>&1 || kill -9 "$pid" >/dev/null 2>&1 || true
  done
}
# Non-loopback outbound TCP held by a PID. Empty output == no egress.
outbound_public() {
  netstat -ano 2>/dev/null | awk -v p="$1" \
    '$NF==p && $1=="TCP" && $4!="LISTENING" && $3 !~ /^127\./ && $3 !~ /^\[::1\]/ {print $0}'
}
json_field()   { sed -n "s/.*\"$2\":\"\\([^\"]*\\)\".*/\\1/p" <<<"$1" | head -1; }
json_number()  { sed -n "s/.*\"$2\":\\([0-9][0-9]*\\).*/\\1/p" <<<"$1" | head -1; }

if [ "${1:-}" = "stop" ]; then
  step "stopping backend :$BACKEND_PORT (Postgres and the inference engine are left running)"
  kill_port "$BACKEND_PORT"
  ok "stopped"
  exit 0
fi

mkdir -p "$LOG_DIR"

# --- 1) find a real engine --------------------------------------------------
# Discovery is deliberately fused with validation: an endpoint only counts as
# "found" once it has passed the not-a-canned-responder battery. That is why the
# demo mock on :11434 can stay in the candidate list without poisoning the run.
step "1/6 locate a real inference engine and prove it is not a canned responder"

engine_chat() { # $1 = base url, $2 = prompt  -> raw JSON on stdout
  curl -s -m "$CHAT_TIMEOUT" --noproxy '*' "$1/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$ENGINE_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"$2\"}],\"max_tokens\":24,\"temperature\":0}" 2>/dev/null
}

# Two prompts with checkable answers, of deliberately different lengths.
Q_ARITH='What is 17 multiplied by 23? Reply with the number only.'
Q_GEO='Name the capital city of Japan. One word only.'

ENGINE_URL=""
CANDIDATES="${PRIVATE_ENGINE_URL:-}"
if [ -z "$CANDIDATES" ]; then
  for p in $ENGINE_PORTS; do CANDIDATES="$CANDIDATES http://127.0.0.1:$p"; done
fi

for cand in $CANDIDATES; do
  curl -s -m 5 --noproxy '*' -o /dev/null "$cand/v1/models" 2>/dev/null || continue
  info "probing $cand ..."

  a_json=$(engine_chat "$cand" "$Q_ARITH")
  g_json=$(engine_chat "$cand" "$Q_GEO")
  a_txt=$(json_field "$a_json" content)
  g_txt=$(json_field "$g_json" content)
  a_tok=$(json_number "$a_json" prompt_tokens)
  g_tok=$(json_number "$g_json" prompt_tokens)

  [ -n "$a_txt" ] && [ -n "$g_txt" ] || { info "  rejected: no completion content"; continue; }
  [ "$a_txt" != "$g_txt" ] || { info "  rejected: identical answer to two different prompts (canned responder)"; continue; }
  grep -q '391' <<<"$a_txt" || { info "  rejected: arithmetic answer wrong ($a_txt)"; continue; }
  grep -qi 'tokyo' <<<"$g_txt" || { info "  rejected: geography answer wrong ($g_txt)"; continue; }
  [ -n "$a_tok" ] && [ -n "$g_tok" ] && [ "$a_tok" != "$g_tok" ] \
    || { info "  rejected: prompt_tokens does not track input length ($a_tok / $g_tok)"; continue; }

  ENGINE_URL="$cand"
  ok "real model at $cand serving '$ENGINE_MODEL'"
  info "Q: $Q_ARITH"
  info "A: $a_txt   (prompt_tokens=$a_tok)"
  info "Q: $Q_GEO"
  info "A: $g_txt   (prompt_tokens=$g_tok)"
  info "two distinct prompts -> two distinct, individually correct answers, with input-dependent token accounting"
  break
done

[ -n "$ENGINE_URL" ] || die "no real inference engine found on: $CANDIDATES
Start an OpenAI-compatible server on a loopback port serving a chat model named
'$ENGINE_MODEL', or point PRIVATE_ENGINE_URL / PRIVATE_ENGINE_MODEL at yours."

ENGINE_PORT="${ENGINE_URL##*:}"
ENGINE_PID="$(pids_on_port "$ENGINE_PORT" | head -1)"
[ -n "$ENGINE_PID" ] || die "could not resolve the engine PID listening on :$ENGINE_PORT"

# --- 2) the engine is local, not a relay ------------------------------------
step "2/6 the engine is on THIS host — loopback bind, no upstream connections"
lan_bind=$(netstat -ano 2>/dev/null | grep 'LISTENING' | grep ":$ENGINE_PORT " | grep -v '127\.0\.0\.1' | grep -v '\[::1\]')
[ -z "$lan_bind" ] || { echo "$lan_bind" | sed 's/^/   /'; die "engine is reachable from outside the host — it must bind loopback only"; }
engine_leaks=$(outbound_public "$ENGINE_PID")
[ -z "$engine_leaks" ] || { echo "$engine_leaks" | sed 's/^/   /'; die "engine holds non-loopback outbound connections — it may be proxying to a remote provider"; }
ok "PID $ENGINE_PID binds 127.0.0.1:$ENGINE_PORT only and holds zero non-loopback outbound connections"
info "the completions above were therefore produced by weights on this machine's disk"

# --- 3) demo Postgres -------------------------------------------------------
step "3/6 demo Postgres (docker $PG_CONTAINER :$PG_PORT)"
if ! docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
  docker start "$PG_CONTAINER" >/dev/null 2>&1 \
    || die "demo Postgres is not running; bring it up with verify-private-endpoint-type.sh first"
fi
for _ in $(seq 1 60); do
  docker exec "$PG_CONTAINER" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$PG_CONTAINER" pg_isready -U postgres >/dev/null 2>&1 || die "Postgres not ready"
ok "Postgres ready on :$PG_PORT"

# --- 4) backend + seed ------------------------------------------------------
step "4/6 backend restarted from source on :$BACKEND_PORT, then seed the real-engine channel"
kill_port "$BACKEND_PORT"
sleep 1
set -a; . "$DEMO_DIR/.env.demo"; set +a
nohup go run ./cmd/server > "$BACKEND_LOG" 2>&1 &
boot_ok=false
for i in $(seq 1 180); do
  if [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://localhost:$BACKEND_PORT/api/status" 2>/dev/null)" = "200" ]; then
    boot_ok=true; ok "backend up after ${i}s"; break
  fi
  sleep 1
done
$boot_ok || { tail -n 30 "$BACKEND_LOG"; die "backend did not boot; see $BACKEND_LOG"; }

docker exec -i "$PG_CONTAINER" psql -U postgres -d newhub -q -v ON_ERROR_STOP=1 \
  -f - < "$DEMO_DIR/seed-private-endpoint-type.sql" >/dev/null \
  || die "seed-private-endpoint-type.sql failed (base seed)"
docker exec -i "$PG_CONTAINER" psql -U postgres -d newhub -q -v ON_ERROR_STOP=1 \
  -f - < "$DEMO_DIR/seed-strict-console-viewer.sql" >/dev/null \
  || die "seed-strict-console-viewer.sql failed"
docker exec -i "$PG_CONTAINER" psql -U postgres -d newhub -q -v ON_ERROR_STOP=1 \
  -v engine_base_url="$ENGINE_URL" -v engine_model="$ENGINE_MODEL" \
  -f - < "$DEMO_DIR/seed-real-engine.sql" >/dev/null \
  || die "seed-real-engine.sql failed"

routed=$(docker exec "$PG_CONTAINER" psql -U postgres -d newhub -tAc \
  "SELECT base_url FROM channels WHERE name = '$REAL_CHANNEL_NAME'" | tr -d '[:space:]')
[ "$routed" = "$ENGINE_URL" ] || die "channel base_url is '$routed', expected '$ENGINE_URL'"
ok "type-57 channel routed to $ENGINE_URL, model '$ENGINE_MODEL' — pure config, no code change"

BACKEND_PID="$(pids_on_port "$BACKEND_PORT" | head -1)"
[ -n "$BACKEND_PID" ] || die "could not resolve backend PID on :$BACKEND_PORT"
info "backend PID = $BACKEND_PID   engine PID = $ENGINE_PID"

# --- 5) the relay calls -----------------------------------------------------
step "5/6 FORWARD — tenant relay call, answered by the local model"
fwd=$(curl -s -m "$CHAT_TIMEOUT" -w $'\n%{http_code}' "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$ENGINE_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"$Q_ARITH\"}],\"max_tokens\":24,\"temperature\":0}")
fwd_code=$(printf '%s' "$fwd" | tail -n1)
fwd_json=$(printf '%s' "$fwd" | sed '$d')
[ "$fwd_code" = "200" ] || { echo "$fwd_json"; die "forward call expected 200, got $fwd_code"; }
fwd_txt=$(json_field "$fwd_json" content)
grep -q '391' <<<"$fwd_txt" \
  || { echo "$fwd_json"; die "the gateway returned 200 but the answer is wrong ('$fwd_txt') — the reply did not come from the model"; }
ok "HTTP 200 through the gateway, answer '$fwd_txt' — computed on-prem"

step "5b/6 a second, open-ended prompt (no canned response can satisfy both)"
biz=$(curl -s -m "$CHAT_TIMEOUT" -w $'\n%{http_code}' "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$ENGINE_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"In one sentence, why would a regulated bank self-host its language model?\"}],\"max_tokens\":64,\"temperature\":0}")
biz_code=$(printf '%s' "$biz" | tail -n1)
biz_json=$(printf '%s' "$biz" | sed '$d')
[ "$biz_code" = "200" ] || { echo "$biz_json"; die "second forward call expected 200, got $biz_code"; }
biz_txt=$(json_field "$biz_json" content)
[ -n "$biz_txt" ] || { echo "$biz_json"; die "second call returned no content"; }
[ "$biz_txt" != "$fwd_txt" ] || die "both prompts produced the same text — this is a canned responder, not a model"
ok "distinct prompt-dependent answer returned"
info "A: $biz_txt"

step "5c/6 STREAMING forward — SSE is the path a real chat UI uses"
# Worth proving rather than asserting: all three dispatch entry points
# (DoApiRequest / DoFormRequest / DoWssRequest) resolve the URL through
# GetRequestURL *before* building the request, and streaming only diverges when
# the response is handled — so the guard covers SSE by construction. "By
# construction" is exactly the kind of claim that is worth one live run.
stream_out=$(curl -s -N -m "$CHAT_TIMEOUT" "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$ENGINE_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"$Q_ARITH\"}],\"max_tokens\":24,\"temperature\":0,\"stream\":true}")
chunks=$(grep -c '^data:' <<<"$stream_out")
[ "$chunks" -ge 2 ] || { echo "$stream_out" | head -5; die "expected an SSE stream, got $chunks data frames"; }
grep -q 'data: \[DONE\]' <<<"$stream_out" || { echo "$stream_out" | tail -3; die "stream did not terminate with [DONE]"; }
stream_txt=$(grep '^data:' <<<"$stream_out" | sed -n 's/.*"content":"\([^"]*\)".*/\1/p' | tr -d '\n')
grep -q '391' <<<"$stream_txt" \
  || { echo "$stream_out" | head -10; die "streamed answer is wrong ('$stream_txt') — the deltas did not come from the model"; }
ok "$chunks SSE frames, reassembled answer '$stream_txt', terminated with [DONE]"

audit_blocked_count() {
  docker exec "$PG_CONTAINER" psql -U postgres -d newhub -tAc \
    "SELECT count(*) FROM audit_events WHERE tenant_id='$TENANT' AND action='security.egress_blocked'" 2>/dev/null | tr -d '[:space:]'
}
audit_before=$(audit_blocked_count)
[ -n "$audit_before" ] || die "audit_events table not reachable — cannot verify the blocked-egress trail"

step "5d/6 NEGATIVE — egress canary (public base_url, DB-inserted past config validation)"
can=$(curl -s -m 30 -w $'\n%{http_code}' "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$CANARY_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"This must never reach a SaaS provider.\"}]}")
can_code=$(printf '%s' "$can" | tail -n1)
can_msg=$(printf '%s' "$can" | sed '$d')
[ "$can_code" = "200" ] && { echo "$can_msg"; die "CANARY WAS SERVED — the egress guard is broken, prompts can leave the network"; }
printf '%s' "$can_msg" | grep -q 'no request was sent' \
  || { echo "$can_msg"; die "canary was refused, but not by the egress guard — check the reason"; }
ok "canary refused (HTTP $can_code) with 'no request was sent' — still blocked with a real model next to it"
info "$can_msg"

step "5e/6 NEGATIVE, STREAMING — the canary must not become reachable by asking for SSE"
# The interesting failure mode: a streaming path that opens the upstream
# connection first and only then discovers it should not have. If that were the
# shape here, the prompt would already be on the wire by the time anything
# refused it, and a 500 would be indistinguishable from a leak.
can_stream=$(curl -s -N -m 30 -w $'\n%{http_code}' "http://localhost:$BACKEND_PORT/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$CANARY_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"This must never reach a SaaS provider, streamed.\"}],\"stream\":true}")
cs_code=$(printf '%s' "$can_stream" | tail -n1)
cs_msg=$(printf '%s' "$can_stream" | sed '$d')
[ "$cs_code" = "200" ] && { echo "$cs_msg"; die "STREAMING CANARY WAS SERVED — the egress guard does not cover SSE"; }
printf '%s' "$cs_msg" | grep -q 'no request was sent' \
  || { echo "$cs_msg"; die "streaming canary refused, but not by the egress guard — check the reason"; }
printf '%s' "$cs_msg" | grep -q '^data:' \
  && { echo "$cs_msg"; die "streaming canary emitted SSE frames — the response began before the guard refused it"; }
ok "streaming canary refused (HTTP $cs_code) before a single SSE frame was emitted"

step "5f/6 AUDIT — the two refusals must be on the record, tenant-attributed"
# A fail-closed guard that leaves no trace is unauditable: an empty log is
# indistinguishable from a guard that never ran. The write is async, so poll.
audit_after=""
for _ in $(seq 1 20); do
  audit_after=$(audit_blocked_count)
  [ -n "$audit_after" ] && [ "$audit_after" -ge $((audit_before + 2)) ] && break
  sleep 1
done
[ -n "$audit_after" ] && [ "$audit_after" -ge $((audit_before + 2)) ] \
  || die "expected 2 more security.egress_blocked events for $TENANT (before=$audit_before after=${audit_after:-none})"
ok "security.egress_blocked recorded for both refusals (before=$audit_before after=$audit_after)"

audit_row=$(docker exec "$PG_CONTAINER" psql -U postgres -d newhub -tAc \
  "SELECT details FROM audit_events WHERE tenant_id='$TENANT' AND action='security.egress_blocked' ORDER BY id DESC LIMIT 1")
printf '%s' "$audit_row" | grep -q '"request_sent":false' \
  || { echo "$audit_row"; die "audit row does not record that no request was sent"; }
printf '%s' "$audit_row" | grep -q '"attempted_host"' \
  || { echo "$audit_row"; die "audit row does not name the attempted host"; }
ok "details name the attempted host and assert request_sent=false"
info "$audit_row"

# The audit table carries a per-tenant tamper-evidence hash chain; a security
# event that lands outside it is weaker evidence than one inside it.
chained=$(docker exec "$PG_CONTAINER" psql -U postgres -d newhub -tAc \
  "SELECT row_hash <> '' FROM audit_events WHERE tenant_id='$TENANT' AND action='security.egress_blocked' ORDER BY id DESC LIMIT 1" | tr -d '[:space:]')
[ "$chained" = "t" ] \
  || die "the blocked-egress event was stored outside the audit hash chain (row_hash empty) — it is not tamper-evident"
ok "event is linked into the per-tenant tamper-evidence hash chain"

step "5g/6 NO EGRESS — gateway AND engine, after real inference has run"
gw_leaks=$(outbound_public "$BACKEND_PID")
[ -z "$gw_leaks" ] || { echo "$gw_leaks" | sed 's/^/   /'; die "gateway holds non-loopback outbound connections"; }
eng_leaks=$(outbound_public "$ENGINE_PID")
[ -z "$eng_leaks" ] || { echo "$eng_leaks" | sed 's/^/   /'; die "engine holds non-loopback outbound connections"; }
ok "zero non-loopback outbound TCP for gateway PID $BACKEND_PID and engine PID $ENGINE_PID"

# --- 6) console -------------------------------------------------------------
step "6/6 CONSOLE — GET /api/v2/$TENANT/private-routing"
BRIDGE_TOKEN="$(grep '^E2E_BRIDGE_TOKEN=' "$DEMO_DIR/.env.demo" | cut -d= -f2-)"
cookie=$(curl -s -i -X POST "http://localhost:$BACKEND_PORT/api/v2/bridge/exchange?token=$BRIDGE_TOKEN&user_id=$VIEWER_USER_ID" \
  | grep -i '^set-cookie:' | sed 's/^[Ss]et-[Cc]ookie: //' | cut -d';' -f1 | head -1)
[ -n "$cookie" ] || die "bridge login did not return a session cookie"
status_json=$(curl -s -m 20 -H "Cookie: $cookie" "http://localhost:$BACKEND_PORT/api/v2/$TENANT/private-routing")
printf '%s' "$status_json" | grep -q '"verdict":"all_traffic_stays_on_prem"' \
  || { echo "$status_json"; die "console verdict is not all_traffic_stays_on_prem"; }
printf '%s' "$status_json" | grep -q "$ENGINE_URL" \
  || { echo "$status_json"; die "console does not list the real engine channel"; }
printf '%s' "$status_json" | grep -q '"will_be_blocked":true' \
  || { echo "$status_json"; die "console does not flag the egress canary as blocked"; }
ok "verdict=all_traffic_stays_on_prem, real engine listed as intranet, canary flagged will_be_blocked"

printf '\n\033[1;32mALL CHECKS PASSED\033[0m — a real model on %s answered tenant %s through the gateway; neither process opened a single non-loopback connection.\n' "$ENGINE_URL" "$TENANT"
printf 'Stop the backend with: bash demo/private-inference/verify-real-engine.sh stop\n'
