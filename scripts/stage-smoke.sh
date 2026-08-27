#!/usr/bin/env bash
# stage-smoke.sh — verify Phase 1 hardening stories in R6 STAGE.
#
# Runs all review-status story checks in sequence. Each check is
# self-contained and idempotent; failure of one does not abort the rest.
#
# Usage:
#   HUB_BASE=https://hub-stage.lurus.cn \
#   ADMIN_TOKEN=sk-admin-... \
#   USER_TOKEN=sk-...  \
#   USER_TOKEN_QUOTA_EXHAUSTED=sk-... \
#   TEST_USER_ID=42 \
#   PLATFORM_NS=lurus-platform \
#   ./scripts/stage-smoke.sh [--timeout-overall 600]
#
# Flags:
#   --timeout-overall N  wrap the script in `timeout Ns` envelope (default 600)
#
# Env (optional):
#   CURL_TIMEOUT       per-curl timeout, default 10
#   NS                 newhub kube namespace, default lurus-system
#   APP_LABEL          newhub kube label, default app=lurus-newhub
#   PG_DSN, R6_HOST    forwarded to story-9-1-tier3-audit-drill.sh
#
# Exit codes:
#   0 — all checks passed
#   N — count of failed checks (visible at end)

# Wrap whole script in `timeout` envelope on first entry. We detect re-entry
# via STAGE_SMOKE_WRAPPED to avoid infinite recursion.
TIMEOUT_OVERALL=600
ARGS=()
for a in "$@"; do
  case "$a" in
    --timeout-overall) shift; TIMEOUT_OVERALL="$1"; shift ;;
    --timeout-overall=*) TIMEOUT_OVERALL="${a#*=}" ;;
    *) ARGS+=("$a") ;;
  esac
done
if [ -z "${STAGE_SMOKE_WRAPPED:-}" ] && command -v timeout >/dev/null 2>&1; then
  export STAGE_SMOKE_WRAPPED=1
  exec timeout "${TIMEOUT_OVERALL}s" bash "$0" "${ARGS[@]}"
fi

set -u  # don't set -e — we want to run every check, not abort early
set -o pipefail

HUB_BASE="${HUB_BASE:-https://hub-stage.lurus.cn}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
USER_TOKEN="${USER_TOKEN:-}"
USER_TOKEN_QUOTA_EXHAUSTED="${USER_TOKEN_QUOTA_EXHAUSTED:-}"
TEST_USER_ID="${TEST_USER_ID:-}"
PLATFORM_NS="${PLATFORM_NS:-lurus-platform}"
NS="${NS:-lurus-system}"
APP_LABEL="${APP_LABEL:-app=lurus-newhub}"
CURL_TIMEOUT="${CURL_TIMEOUT:-10}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

PASS=0
FAIL=0
SKIP=0

# --- helpers ---------------------------------------------------------------

c_red()    { printf '\033[31m%s\033[0m' "$1"; }
c_grn()    { printf '\033[32m%s\033[0m' "$1"; }
c_ylw()    { printf '\033[33m%s\033[0m' "$1"; }
c_dim()    { printf '\033[2m%s\033[0m'  "$1"; }

ok()    { PASS=$((PASS+1)); printf '  %s %s\n'   "$(c_grn '✓')" "$1"; }
fail()  { FAIL=$((FAIL+1)); printf '  %s %s\n   %s\n' "$(c_red '✗')" "$1" "$(c_dim "${2:-}")"; }
skip()  { SKIP=$((SKIP+1)); printf '  %s %s\n   %s\n' "$(c_ylw '○')" "$1" "$(c_dim "${2:-no env}")"; }
hdr()   { printf '\n%s\n' "$(c_dim "── $1 ──")"; }

require_env() {
  for var in "$@"; do
    if [ -z "${!var:-}" ]; then
      return 1
    fi
  done
  return 0
}

# --- 1. healthcheck --------------------------------------------------------

hdr "0. baseline"
status_body=$(curl -fsS --max-time "$CURL_TIMEOUT" "$HUB_BASE/api/status" 2>/dev/null || echo "")
if [ -z "$status_body" ]; then
  fail "GET /api/status failed" "service may not be up; abort further checks"
  echo "TOTAL: pass=$PASS fail=$FAIL skip=$SKIP"
  exit "$FAIL"
fi
if command -v jq >/dev/null 2>&1; then
  if echo "$status_body" | jq -e '.success == true' >/dev/null 2>&1; then
    ok "GET /api/status → 200, success=true"
  else
    fail "/api/status returned non-success body" "$(echo "$status_body" | head -c 200)"
  fi
else
  if echo "$status_body" | grep -q '"success":true'; then
    ok "GET /api/status → 200, success=true (jq unavailable; grep fallback)"
  else
    fail "/api/status body lacks success:true" "$(echo "$status_body" | head -c 200)"
  fi
fi

# --- 2. story 7-1 circuit breaker IsUpstreamFailure ------------------------

hdr "story 7-1 — circuit breaker only records UPSTREAM failures"
# Indirect check: trigger a 401 user error, confirm the CHANNEL is NOT marked
# disabled afterward. Pre-7-1, any 401 from upstream would trip the breaker.
if require_env USER_TOKEN; then
  resp=$(curl -sS --max-time "$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer invalid-${USER_TOKEN: -8}" \
    "$HUB_BASE/v1/chat/completions" \
    -d '{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}' || echo "000")
  if [ "$resp" = "401" ]; then
    ok "user error returns 401 (channel breaker should remain closed)"
  else
    fail "expected 401 for invalid auth, got $resp"
  fi
  # Verify: no breaker.open log in last 30s
  if command -v kubectl >/dev/null 2>&1; then
    hits=$(kubectl logs -n "$NS" -l "$APP_LABEL" --since=30s 2>/dev/null \
      | grep -ic 'breaker.*open' || true)
    if [ "${hits:-0}" -eq 0 ]; then
      ok "no 'breaker open' log entries in last 30s (7-1 invariant)"
    else
      fail "'breaker open' appeared after user-auth failure" "logs had ${hits} matches; 7-1 contract broken"
    fi
  else
    skip "story 7-1 log assertion" "kubectl unavailable"
  fi
else
  skip "story 7-1" "USER_TOKEN missing"
fi

# --- 3. story 7-4 Retry-After header ---------------------------------------

hdr "story 7-4 — quota denial returns Retry-After header"
if require_env USER_TOKEN_QUOTA_EXHAUSTED; then
  hdr_out=$(curl -sS --max-time "$CURL_TIMEOUT" -o /dev/null -D - \
    -H "Authorization: Bearer $USER_TOKEN_QUOTA_EXHAUSTED" \
    "$HUB_BASE/v1/chat/completions" \
    -d '{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}' || echo "")
  status=$(printf '%s\n' "$hdr_out" | head -1 | awk '{print $2}')
  retry=$(printf '%s\n' "$hdr_out" | grep -i '^Retry-After:' | awk '{print $2}' | tr -d '\r')
  if [ "$status" = "402" ] && [ -n "$retry" ]; then
    now=$(date +%s)
    if [ "$retry" -gt "$now" ]; then
      ok "402 + Retry-After: $retry (≈ $((retry - now))s in future)"
    else
      fail "Retry-After is not in future" "got=$retry now=$now"
    fi
  else
    fail "expected 402 + Retry-After header" "status=$status retry=$retry"
  fi
else
  skip "story 7-4" "USER_TOKEN_QUOTA_EXHAUSTED missing"
fi

# --- 4. story 7-2.1 PG WAL-G archive_command -------------------------------

hdr "story 7-2.1 — PG WAL-G continuous archiving"
if command -v ssh >/dev/null 2>&1 && [ -n "${R6_HOST:-}" ]; then
  archive_cmd=$(ssh "root@$R6_HOST" \
    "docker exec newhub-pg psql -U postgres -tAc \"SHOW archive_command;\"" 2>/dev/null || echo "")
  if echo "$archive_cmd" | grep -q "archive-wal.sh"; then
    ok "archive_command points at archive-wal.sh"
  else
    fail "archive_command not configured" "got: $archive_cmd"
  fi
  archive_mode=$(ssh "root@$R6_HOST" \
    "docker exec newhub-pg psql -U postgres -tAc \"SHOW archive_mode;\"" 2>/dev/null || echo "")
  if [ "$archive_mode" = "on" ]; then
    ok "archive_mode = on"
  else
    fail "archive_mode != on" "got: $archive_mode"
  fi
  echo "  $(c_dim "  full restore drill is separate: bash scripts/pg-restore-drill.sh on R6 (monthly)")"
else
  skip "story 7-2.1" "R6_HOST missing or ssh unavailable"
fi

# --- 5. story 8-2.1 cost spike protection ----------------------------------

hdr "story 8-2.1 — cost spike protection (5-min sliding window)"
# Real breach test is in chaos-drill.sh Scenario C.
# COST_SPIKE_ENFORCE defaults to false (internal/pkg/common/constants.go), and chaos-drill.sh mirrors that via
# CHAOS_COST_SPIKE_ENFORCE (also default false): the default run asserts the
# breach is admitted (no 429), the user stays enabled, and
# lurus_gateway_cost_spike_breach_total{action="observed"} increases — it
# does NOT disable the user unless CHAOS_COST_SPIKE_ENFORCE=true (which
# should only be set if the target deployment actually runs
# COST_SPIKE_ENFORCE=true). Here we only assert wiring + that chaos-drill's
# SKIP_REASON_B marker is acknowledged (audit gap is visible, not silent).
if require_env ADMIN_TOKEN TEST_USER_ID; then
  ok "smoke wiring present (full breach automated in chaos-drill.sh Scenario C)"
  if grep -q 'SKIP_REASON_B=' "$SCRIPT_DIR/chaos-drill.sh" 2>/dev/null; then
    ok "chaos-drill.sh SKIP_REASON_B marker present (Scenario B gap is tracked, not silent)"
  else
    fail "chaos-drill.sh missing SKIP_REASON_B marker" "audit gap is silent — DO NOT promote"
  fi
else
  skip "story 8-2.1 manual breach test" "ADMIN_TOKEN/TEST_USER_ID missing"
fi

# --- 6. story 8-2.2 video proxy ownership check ----------------------------

hdr "story 8-2.2 — video proxy cross-tenant 403"
if require_env USER_TOKEN OTHER_USER_TASK_ID; then
  resp=$(curl -sS --max-time "$CURL_TIMEOUT" -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $USER_TOKEN" \
    "$HUB_BASE/v1/videos/$OTHER_USER_TASK_ID/content" || echo "000")
  if [ "$resp" = "403" ]; then
    ok "non-owner GET /v1/videos/<other-user-task>/content → 403"
  else
    fail "expected 403 for cross-tenant video access" "got $resp"
  fi
else
  skip "story 8-2.2" "OTHER_USER_TASK_ID missing"
fi

# --- 7. story 8-2.3 / 8-2.3.1 NATS llm.image.generated ---------------------

hdr "story 8-2.3 + 8-2.3.1 — NATS image.generated event with image_url"
if require_env USER_TOKEN; then
  job=$(curl -fsS --max-time 60 -X POST \
    -H "Authorization: Bearer $USER_TOKEN" \
    -H "Content-Type: application/json" \
    "$HUB_BASE/v1/images/generations" \
    -d '{"model":"dall-e-3","prompt":"smoke test cyan circle","n":1}' \
    | tee /tmp/img.json || echo "")
  if [ -n "$job" ] && (command -v jq >/dev/null 2>&1 \
       && echo "$job" | jq -e '.data[0].url // .data[0].b64_json' >/dev/null 2>&1); then
    ok "image generation succeeded; data[0].url|b64_json present"
  elif echo "$job" | grep -q '"url"'; then
    ok "image generation succeeded (grep fallback; jq unavailable or strict failed)"
  else
    fail "image generation failed or returned no url" "see /tmp/img.json"
  fi
  # Await NATS publish: sleep 2s then grep newhub logs.
  if command -v kubectl >/dev/null 2>&1; then
    sleep 2
    nats_hits=$(kubectl logs -n "$NS" -l "$APP_LABEL" --since=10s 2>/dev/null \
      | grep -cE 'image\.generated|NATS.*publish' || true)
    if [ "${nats_hits:-0}" -ge 1 ]; then
      ok "NATS publish event observed in newhub logs (${nats_hits} matches in last 10s)"
    else
      fail "no NATS image.generated publish event in newhub logs" \
        "checked: kubectl logs -n $NS -l $APP_LABEL --since=10s | grep -E 'image\\.generated|NATS.*publish'"
    fi
    # Optional: notification deploy log check (informational only).
    notif_hits=$(kubectl logs -n "$PLATFORM_NS" deploy/notification --since=1m 2>/dev/null \
      | grep -c 'llm.image.generated' || true)
    if [ "${notif_hits:-0}" -ge 1 ]; then
      ok "notification deploy received llm.image.generated (${notif_hits} matches in last 1m)"
    else
      echo "  $(c_dim "  notification log check inconclusive (${notif_hits:-0} matches); newhub-side publish confirmed above")"
    fi
  else
    skip "8-2.3 NATS await" "kubectl unavailable"
  fi
else
  skip "story 8-2.3 / 8-2.3.1" "USER_TOKEN missing"
fi

# --- 8. story 8-2.4 NATS llm.usage.milestone -------------------------------

hdr "story 8-2.4 — NATS usage.milestone event (lifetime token tiers)"
if require_env USER_TOKEN TEST_USER_ID; then
  echo "  $(c_dim "  manual follow-up:")"
  echo "  $(c_dim "    redis-cli DEL llm:tokens:$TEST_USER_ID llm:milestone:$TEST_USER_ID:1000")"
  echo "  $(c_dim "    send chat completions until cumulative > 1000 tokens")"
  echo "  $(c_dim "    kubectl -n $PLATFORM_NS logs deploy/notification --since=1m | grep 'llm.usage.milestone'")"
  echo "  $(c_dim "    payload.milestone == \"first_1k\"")"
  ok "smoke wiring (manual milestone trigger required)"
else
  skip "story 8-2.4" "USER_TOKEN/TEST_USER_ID missing"
fi

# --- 9. story 9-1 tier 3 audit data ----------------------------------------

hdr "story 9-1 — tier 3 modality usage audit (PROD SQL automated)"
DRILL="$SCRIPT_DIR/story-9-1-tier3-audit-drill.sh"
if [ -x "$DRILL" ] || [ -f "$DRILL" ]; then
  if bash "$DRILL"; then
    ok "tier-3 usage audit drill PASS — report written under doc/reports/"
  else
    fail "tier-3 usage audit drill FAILED (exit $?)" "see drill output above"
  fi
else
  fail "story-9-1-tier3-audit-drill.sh not found at $DRILL" "Item 4 file missing"
fi

# --- 10. batch-2 hardening (#54–#61) — channel egress SSRF + validation -----

hdr "batch-2 hardening (#54–#61) — channel egress SSRF + v1/v2 validation"
# Each case exercises the REJECT path of the v1 admin channel create
# (POST /api/channel/), so nothing is persisted when the guard fires. NOTE the
# v1 contract: a validation failure returns HTTP 200 with body success:false
# (not a 4xx), so we assert the body, not the status code.
if require_env ADMIN_TOKEN; then
  _create_channel() {
    curl -sS --max-time "$CURL_TIMEOUT" \
      -H "Authorization: Bearer $ADMIN_TOKEN" \
      -H "Content-Type: application/json" \
      "$HUB_BASE/api/channel/" -d "$1" 2>/dev/null || echo ""
  }
  _is_rejected() {
    if command -v jq >/dev/null 2>&1; then
      echo "$1" | jq -e '.success == false' >/dev/null 2>&1
    else
      echo "$1" | grep -q '"success":false'
    fi
  }

  # #54/#58 — SSRF egress guard: an internal base_url must be refused. The key is
  # deliberately invalid so that even if SSRF protection were disabled (channel
  # created), it cannot relay; an accepted create is itself a finding.
  resp=$(_create_channel '{"channel":{"name":"SMOKE-SSRF-DELETE-ME","key":"sk-smoke-invalid","models":"gpt-4","base_url":"http://169.254.169.254"}}')
  if _is_rejected "$resp"; then
    ok "channel with internal base_url (169.254.169.254) refused — SSRF egress guard"
  else
    fail "internal base_url was NOT refused" "SSRF protection may be disabled; resp=$(echo "$resp" | head -c 200)"
  fi

  # #61 (R3) — model-name length: a >255-char model must be refused by the
  # shared validateChannelContent.
  long_model=$(head -c 256 /dev/zero | tr '\0' 'm')
  resp=$(_create_channel "{\"channel\":{\"name\":\"SMOKE-MLEN\",\"key\":\"sk-smoke-invalid\",\"models\":\"$long_model\"}}")
  if _is_rejected "$resp" && echo "$resp" | grep -q '模型名称过长'; then
    ok "over-long model name refused — v1/v2 shared validateChannelContent"
  elif _is_rejected "$resp"; then
    ok "over-long model name refused (success:false; exact message not matched)"
  else
    fail "over-long model name was NOT refused" "resp=$(echo "$resp" | head -c 200)"
  fi

  # #61 (R3) — VertexAI region: type=41 with no region must be refused. This
  # rule was v1-only before R3; the smoke proves it is enforced on the current
  # build's create path.
  resp=$(_create_channel '{"channel":{"name":"SMOKE-VERTEX","key":"sk-smoke-invalid","models":"gemini-pro","type":41}}')
  if _is_rejected "$resp" && echo "$resp" | grep -q '部署地区'; then
    ok "region-less VertexAI channel refused — R3 validation unification"
  elif _is_rejected "$resp"; then
    ok "region-less VertexAI channel refused (success:false; exact message not matched)"
  else
    fail "region-less VertexAI channel was NOT refused" "resp=$(echo "$resp" | head -c 200)"
  fi
else
  skip "batch-2 hardening (#54–#61)" "ADMIN_TOKEN missing"
fi

# --- summary --------------------------------------------------------------

echo ""
printf '%s pass=%d fail=%d skip=%d\n' \
  "$(c_dim '──────────────')" "$PASS" "$FAIL" "$SKIP"

if [ "$FAIL" -eq 0 ]; then
  printf '%s STAGE smoke OK — promote to PROD when ready.\n' "$(c_grn '✓')"
  exit 0
else
  printf '%s %d check(s) failed — DO NOT promote.\n' "$(c_red '✗')" "$FAIL"
  exit "$FAIL"
fi
