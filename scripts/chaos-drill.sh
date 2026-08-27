#!/usr/bin/env bash
# chaos-drill.sh — fault-injection drill for circuit breaker (7-1) +
# Retry-After (7-4) + cost spike (8-2.1).
#
# Validates that newhub responds correctly when an upstream provider
# misbehaves. Intended for STAGE only — DO NOT run against PROD.
#
# What it does:
#   Scenario A: pick a tier-2 channel, inject 5xx via channel-test/admin
#               override, send 5 requests, confirm breaker opens after the
#               threshold and closes after cool-down. Asserts:
#                 - HTTP status codes (≥3 5xx of 5)
#                 - Prometheus circuit_breaker_state{channel_id="..."} → 1
#                 - kubectl log tail contains "breaker.*open"
#   Scenario B: slow-loris request (300s no body). Not automated in this
#               script (needs slow upstream mock). Marked SKIP with reason
#               in SKIP_REASON_B at the top of the script. Wrappers MUST
#               check this marker.
#   Scenario C: send up to 100 quick requests to breach the 5-minute cost
#               window. What gets asserted depends on
#               CHAOS_COST_SPIKE_ENFORCE (default: false, mirroring the
#               deployed COST_SPIKE_ENFORCE default — see
#               internal/pkg/common/constants.go): false (the default)
#               asserts the requests are all admitted (no 429), the user
#               stays enabled, and
#               lurus_gateway_cost_spike_breach_total{action="observed"}
#               increases in Prometheus; true asserts the legacy 429 +
#               auto-disable behavior and re-enables the user on exit. This
#               script cannot change the remote deployment's env — it only
#               picks which assertion matches what that deployment is
#               actually configured to do.
#
# Required env:
#   HUB_BASE, ADMIN_TOKEN, USER_TOKEN, CHAOS_CHANNEL_ID, TEST_USER_ID
#
# Optional env:
#   PROM_URL  Prometheus base URL (default: http://prometheus.observability.svc:9090)
#             If unset AND not reachable, prom assertions print ⚠️  skip.
#   NS        Kubernetes namespace for newhub logs (default: lurus-system)
#   APP_LABEL Kubernetes label selector (default: app=lurus-newhub)
#   CHAOS_COST_SPIKE_ENFORCE  "true" if the target deployment has
#             COST_SPIKE_ENFORCE=true; drives which Scenario C assertion
#             runs (see above). Default: false.
#
# Usage:
#   HUB_BASE=https://hub-stage.lurus.cn \
#   ADMIN_TOKEN=sk-admin-... \
#   USER_TOKEN=sk-... \
#   CHAOS_CHANNEL_ID=99 \
#   TEST_USER_ID=42 \
#   ./scripts/chaos-drill.sh

set -euo pipefail

# ────────────────────────────────────────────────────────────────────────
# Blocking marker for unautomated Scenario B. Wrappers (e.g. stage-smoke.sh)
# MUST grep for this string and treat it as a known gap; do NOT silently
# accept a green chaos-drill if SKIP_REASON_B is non-empty.
# Audit ref: 2026-05-20 squad 2A.
# ────────────────────────────────────────────────────────────────────────
SKIP_REASON_B="Scenario B (slow-loris 524) requires a controllable slow upstream mock; \
not yet wired in STAGE. Manual verification required: point CHAOS_CHANNEL_ID base_url \
at httpbin.org/delay/300, send 1 request with --max-time 60, expect 524 + breaker increment."

HUB_BASE="${HUB_BASE:-https://hub-stage.lurus.cn}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
USER_TOKEN="${USER_TOKEN:-}"
CHAOS_CHANNEL_ID="${CHAOS_CHANNEL_ID:-}"
TEST_USER_ID="${TEST_USER_ID:-}"
PROM_URL="${PROM_URL:-http://prometheus.observability.svc:9090}"
NS="${NS:-lurus-system}"
APP_LABEL="${APP_LABEL:-app=lurus-newhub}"
CHAOS_COST_SPIKE_ENFORCE="${CHAOS_COST_SPIKE_ENFORCE:-false}"

if [ -z "$ADMIN_TOKEN" ] || [ -z "$USER_TOKEN" ] || [ -z "$CHAOS_CHANNEL_ID" ] || [ -z "$TEST_USER_ID" ]; then
  echo "ERROR: missing required env. Set ADMIN_TOKEN, USER_TOKEN, CHAOS_CHANNEL_ID, TEST_USER_ID."
  exit 2
fi

if [ "${HUB_BASE}" = "https://hub.lurus.cn" ] || [ "${HUB_BASE}" = "https://api.lurus.cn" ]; then
  echo "REFUSING: HUB_BASE looks like PROD ($HUB_BASE). Chaos drill is STAGE-only."
  exit 2
fi

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); printf '\033[32m  ✓\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL+1)); printf '\033[31m  ✗\033[0m %s\n     %s\n' "$1" "${2:-}"; }
warn() { printf '\033[33m  ⚠\033[0m %s\n     %s\n' "$1" "${2:-}"; }
hdr()  { printf '\n\033[2m── %s ──\033[0m\n' "$1"; }

# ---- prometheus helper ---------------------------------------------------
# prom_query <promql> <op:eq|ge|gt> <expected_val>
# Returns 0 on assertion pass, 1 on fail, 2 on prom unreachable.
prom_query() {
  local query="$1" op="$2" expected="$3"
  local resp
  resp=$(curl -sS --max-time 10 -G --data-urlencode "query=${query}" \
    "${PROM_URL}/api/v1/query" 2>/dev/null) || {
    warn "Prometheus unreachable at ${PROM_URL}" "skipping assertion: ${query}"
    return 2
  }
  if ! command -v jq >/dev/null 2>&1; then
    warn "jq not installed" "skipping assertion: ${query}"
    return 2
  fi
  local status val
  status=$(echo "$resp" | jq -r '.status // "error"')
  if [ "$status" != "success" ]; then
    warn "Prometheus query error" "$resp"
    return 2
  fi
  val=$(echo "$resp" | jq -r '.data.result[0].value[1] // "null"')
  if [ "$val" = "null" ]; then
    fail "prom_query returned no series" "query=${query}"
    return 1
  fi
  case "$op" in
    eq) awk -v a="$val" -v b="$expected" 'BEGIN{exit !(a==b)}' && return 0 || return 1 ;;
    ge) awk -v a="$val" -v b="$expected" 'BEGIN{exit !(a>=b)}' && return 0 || return 1 ;;
    gt) awk -v a="$val" -v b="$expected" 'BEGIN{exit !(a>b)}'  && return 0 || return 1 ;;
    *)  fail "prom_query unknown op: $op"; return 1 ;;
  esac
}

# prom_value <promql> — echoes the raw scalar value for a query, or an empty
# string if Prometheus/jq is unavailable or the series has no data. Unlike
# prom_query this never calls warn/fail itself; callers that need a before/
# after delta (Scenario C's observe-mode counter check) decide how to handle
# emptiness.
prom_value() {
  local query="$1"
  local resp
  resp=$(curl -sS --max-time 10 -G --data-urlencode "query=${query}" \
    "${PROM_URL}/api/v1/query" 2>/dev/null) || { echo ""; return; }
  command -v jq >/dev/null 2>&1 || { echo ""; return; }
  local status
  status=$(echo "$resp" | jq -r '.status // "error"')
  [ "$status" = "success" ] || { echo ""; return; }
  echo "$resp" | jq -r '.data.result[0].value[1] // ""'
}

# ---- Scenario A: 5xx injection → breaker opens ----------------------------

hdr "Scenario A — repeated 5xx from upstream → breaker opens"

# Capture original channel definition so we can restore on exit.
ORIG_CHANNEL=$(curl -sS --max-time 10 -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$HUB_BASE/api/channel/$CHAOS_CHANNEL_ID" || echo "")
ORIG_BASE_URL=$(echo "$ORIG_CHANNEL" | (command -v jq >/dev/null && jq -r '.data.base_url // ""') 2>/dev/null || echo "")
echo "  $(printf '\033[2m[snapshot] channel %s captured (base_url=%s, will restore on exit)\033[0m\n' "$CHAOS_CHANNEL_ID" "${ORIG_BASE_URL:-<unknown>}")"

restore_channel() {
  echo ""
  echo "  $(printf '\033[2m[cleanup] restoring channel %s base_url=%s\033[0m\n' "$CHAOS_CHANNEL_ID" "$ORIG_BASE_URL")"
  # Re-enable channel and restore base_url. Best-effort; do not abort on error.
  local restore_body
  restore_body=$(printf '{"id":%s,"base_url":%s,"status":1}' "$CHAOS_CHANNEL_ID" "$(printf '%s' "${ORIG_BASE_URL:-}" | (command -v jq >/dev/null && jq -Rs . || printf '"%s"' "${ORIG_BASE_URL:-}"))")
  curl -sS --max-time 10 -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    "$HUB_BASE/api/channel/" -d "$restore_body" >/dev/null 2>&1 || true
  # Verify enabled flag back on
  local verify
  verify=$(curl -sS --max-time 10 -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$HUB_BASE/api/channel/$CHAOS_CHANNEL_ID" 2>/dev/null || echo "")
  if echo "$verify" | grep -q '"status":1'; then
    printf '\033[32m  ✓\033[0m channel %s re-enabled (status=1)\n' "$CHAOS_CHANNEL_ID"
  else
    printf '\033[31m  ✗\033[0m channel %s re-enable verification failed; check admin UI\n' "$CHAOS_CHANNEL_ID"
  fi

  # Re-enable test user (Scenario C disables them)
  curl -sS --max-time 10 -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    "$HUB_BASE/api/user/manage" \
    -d "{\"id\":$TEST_USER_ID,\"action\":\"enable\"}" >/dev/null 2>&1 || true
}
trap restore_channel EXIT

# Inject a base_url that always 502s (httpbin.org/status/502)
inject=$(curl -sS --max-time 10 -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  "$HUB_BASE/api/channel/" \
  -d "{\"id\":$CHAOS_CHANNEL_ID,\"base_url\":\"https://httpbin.org/status/502\"}")
if echo "$inject" | grep -q '"success":true'; then
  ok "channel $CHAOS_CHANNEL_ID base_url overridden to 502 source"
else
  fail "could not inject — channel update API rejected" "$inject"
  exit "$FAIL"
fi

# Send 5 requests with bounded backoff; expect breaker to open after threshold.
opens=0
delay=1
for i in 1 2 3 4 5; do
  code=$(curl -sS --max-time 30 -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $USER_TOKEN" \
    "$HUB_BASE/v1/chat/completions" \
    -d '{"model":"gpt-4","messages":[{"role":"user","content":"chaos-drill"}]}')
  printf "    req %d → %s (sleep %ds before next)\n" "$i" "$code" "$delay"
  if [ "$code" = "503" ] || [ "$code" = "502" ]; then
    opens=$((opens+1))
  fi
  sleep "$delay"
  # Exponential backoff capped at 4s — gives breaker time to record state.
  if [ "$delay" -lt 4 ]; then delay=$((delay*2)); fi
done
if [ "$opens" -ge 3 ]; then
  ok "≥3/5 requests returned 5xx (breaker fired or upstream propagated)"
else
  fail "expected ≥3 5xx responses" "got $opens"
fi

# Prom assertion: circuit_breaker_state for this channel should be 1 (open).
# Metric name confirmed: lurus_gateway_circuit_breaker_state{channel_id="..."}
# Source: internal/pkg/metrics/metrics.go:161-169
prom_query "lurus_gateway_circuit_breaker_state{channel_id=\"${CHAOS_CHANNEL_ID}\"}" eq 1
rc=$?
case "$rc" in
  0) ok "Prometheus: circuit_breaker_state{channel_id=$CHAOS_CHANNEL_ID} == 1 (open)" ;;
  1) fail "Prometheus: breaker state != 1 for channel $CHAOS_CHANNEL_ID" "expected breaker to open" ;;
  2) : ;; # already warned, skip
esac

# Log tail assertion: confirm breaker open event was logged.
if command -v kubectl >/dev/null 2>&1; then
  log_hits=$(kubectl logs -n "$NS" -l "$APP_LABEL" --tail=200 --since=2m 2>/dev/null \
    | grep -ic 'breaker.*open' || true)
  if [ "${log_hits:-0}" -ge 1 ]; then
    ok "kubectl logs: found ${log_hits} 'breaker open' event(s) in last 2m"
  else
    fail "kubectl logs: no 'breaker open' events in last 2m" \
      "tried: kubectl logs -n $NS -l $APP_LABEL --tail=200 --since=2m | grep -i 'breaker.*open'"
  fi
else
  warn "kubectl unavailable" "cannot verify 'breaker open' log event"
fi

# ---- Scenario B: slow-loris timeout (524) ---------------------------------

hdr "Scenario B — upstream timeout returns 524 + breaker records"
warn "Scenario B SKIPPED" "$SKIP_REASON_B"

# ---- Scenario C: cost spike (enforce → disabled / observe → counted) -----

hdr "Scenario C — cost spike protection (8-2.1), CHAOS_COST_SPIKE_ENFORCE=$CHAOS_COST_SPIKE_ENFORCE"
if [ "$CHAOS_COST_SPIKE_ENFORCE" = "true" ]; then
  echo "  $(printf '\033[2m  NOTE: this disables TEST_USER_ID=%s. Will be auto-re-enabled on exit via trap.\033[0m\n' "$TEST_USER_ID")"
else
  echo "  $(printf '\033[2m  NOTE: COST_SPIKE_ENFORCE defaults to false (observe mode) — requests are expected to be ADMITTED, not blocked. Set CHAOS_COST_SPIKE_ENFORCE=true only if the target deployment actually runs with COST_SPIKE_ENFORCE=true.\033[0m\n')"
fi

# Auto-skip via env if running unattended; otherwise prompt.
if [ "${CHAOS_AUTO_C:-0}" = "1" ] || [ ! -t 0 ]; then
  confirm="y"
  echo "  $(printf '\033[2m  CHAOS_AUTO_C=1 or non-tty — proceeding without prompt\033[0m\n')"
else
  read -r -p "  proceed? [y/N] " confirm
fi

if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
  echo "  skipped Scenario C"
elif [ "$CHAOS_COST_SPIKE_ENFORCE" = "true" ]; then
  echo "  sending up to 100 quick chat-completion requests..."
  burst=0
  for i in $(seq 1 100); do
    code=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer $USER_TOKEN" \
      "$HUB_BASE/v1/chat/completions" \
      -d '{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"x"}],"max_tokens":1000}' || echo "000")
    if [ "$code" = "429" ]; then
      ok "request $i hit 429 — cost spike triggered"
      burst=$i
      break
    fi
  done
  if [ "$burst" = 0 ]; then
    fail "no 429 after 100 requests" "threshold may be too high, middleware not wired, or the deployment isn't actually running COST_SPIKE_ENFORCE=true (CHAOS_COST_SPIKE_ENFORCE=true assumed it was)"
  fi

  # Verify user disabled
  user=$(curl -sS --max-time 10 -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$HUB_BASE/api/user/$TEST_USER_ID")
  if echo "$user" | grep -q '"status":2'; then
    ok "user $TEST_USER_ID auto-disabled (status=2)"
  else
    fail "user not disabled" "$user"
  fi

  # Actual re-enable (trap will also re-attempt; this gives immediate feedback).
  reenable=$(curl -sS --max-time 10 -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    "$HUB_BASE/api/user/manage" \
    -d "{\"id\":$TEST_USER_ID,\"action\":\"enable\"}" || echo "")
  verify=$(curl -sS --max-time 10 -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$HUB_BASE/api/user/$TEST_USER_ID")
  if echo "$verify" | grep -q '"status":1'; then
    ok "user $TEST_USER_ID re-enabled (status=1)"
  else
    fail "user re-enable verification failed" "$verify"
  fi
else
  echo "  sending 100 quick chat-completion requests (observe mode: all must be admitted)..."
  # sum() is load-bearing, not cosmetic: r6-stage runs replicas=3, so this
  # counter has one series per pod and the burst below round-robins across
  # them. prom_value reads .data.result[0], so an unaggregated query would
  # compare two arbitrary and possibly different pods' series and could go
  # both false-red and false-green. The alert rules query it the same way.
  breach_query='sum(lurus_gateway_cost_spike_breach_total{action="observed"})'
  before=$(prom_value "$breach_query")
  [ -z "$before" ] && before=0

  sent=0
  saw429=0
  for i in $(seq 1 100); do
    code=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer $USER_TOKEN" \
      "$HUB_BASE/v1/chat/completions" \
      -d '{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"x"}],"max_tokens":1000}' || echo "000")
    sent=$i
    if [ "$code" = "429" ]; then
      saw429=1
      fail "request $i got 429 in observe mode" "COST_SPIKE_ENFORCE=false must never 429 — either the deployment is actually running enforce mode (rerun with CHAOS_COST_SPIKE_ENFORCE=true) or this is a real regression"
      break
    fi
  done
  if [ "$saw429" = 0 ]; then
    ok "$sent requests admitted, none returned 429 (observe mode does not block)"
  fi

  # Poll rather than sleep a fixed 2s. There is no 2-second scrape anywhere in
  # this repo — the rule groups in deploy/ are interval:30s — so a short fixed
  # sleep re-reads the pre-burst value and fails a healthy system. Bounded at
  # ~70s (two full 30s intervals plus slack) so a genuinely stuck counter still
  # terminates the drill instead of hanging it.
  after="$before"
  for _ in $(seq 1 14); do
    sleep 5
    probe=$(prom_value "$breach_query")
    [ -n "$probe" ] && after="$probe"
    awk -v a="$after" -v b="$before" 'BEGIN{exit !(a>b)}' && break
  done

  if [ -z "$after" ]; then
    warn "Prometheus unreachable or cost_spike_breach_total{action=observed} has no series" "cannot confirm the breach was counted — check PROM_URL"
  elif awk -v a="$after" -v b="$before" 'BEGIN{exit !(a>b)}'; then
    ok "sum(lurus_gateway_cost_spike_breach_total{action=observed}) increased ($before -> $after)"
  elif [ "$before" = "0" ] && [ "$after" = "0" ]; then
    # Not a failure: the cost-spike window is only written for wallet-linked
    # tokens (both RecordCostSpikeWindow call sites sit inside
    # `relayInfo.IdentityAccountID > 0`), and console-created tokens do not set
    # identity_account_id. A drill token that is not wallet-linked therefore
    # never accumulates a window, so the counter cannot move and that says
    # nothing about the middleware. Downgrading to warn keeps the drill honest
    # instead of red-by-configuration.
    warn "breach counter stayed at 0 — cannot exercise the observe path with this token" \
      "the 5-minute window is only recorded when the token is wallet-linked (identity_account_id > 0); check TEST_USER_ID's token before treating this as a defect"
  else
    fail "sum(lurus_gateway_cost_spike_breach_total{action=observed}) did not increase" "before=$before after=$after"
  fi

  # Observe mode must never disable the account.
  user=$(curl -sS --max-time 10 -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$HUB_BASE/api/user/$TEST_USER_ID")
  if echo "$user" | grep -q '"status":2'; then
    fail "user $TEST_USER_ID was disabled in observe mode" "$user"
  else
    ok "user $TEST_USER_ID remains enabled (observe mode never disables)"
  fi
fi

# ---- summary -------------------------------------------------------------

echo ""
printf '\033[2m──────────────\033[0m pass=%d fail=%d\n' "$PASS" "$FAIL"
echo "  $(printf '\033[2m  Scenario B remains SKIPPED — see SKIP_REASON_B in script header\033[0m\n')"
if [ "$FAIL" -eq 0 ]; then
  printf '\033[32m✓\033[0m chaos drill complete (Scenario B not automated)\n'
  exit 0
else
  printf '\033[31m✗\033[0m %d scenario(s) failed\n' "$FAIL"
  exit "$FAIL"
fi
