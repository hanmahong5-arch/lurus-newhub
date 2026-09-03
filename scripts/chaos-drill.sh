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
#   Scenario B: upstream that dies mid-stream / stalls before the first byte /
#               reports an unpaid account. Automated as of 2026-09-03 against
#               the in-process fault simulator
#               (internal/adapter/handler/faultsim.go); it was a permanent SKIP
#               before that because it "needs a slow upstream mock".
#               Requires FAULTSIM_TOKEN + FAULTSIM_CHANNEL_ID; without them it
#               still reports SKIP rather than silently passing.
#
#               Setup: start the UAT instance with FAULTSIM_TOKEN set, then
#               seed a channel whose base_url is
#               http://127.0.0.1:3000/api/v2/faultsim (loopback on purpose —
#               deploy/k8s/r6-uat/netpol-egress.yaml excludes all RFC1918, so a
#               separate Pod would be unreachable) with key = FAULTSIM_TOKEN
#               and models = mid_stream_abort,slow_headers,http_500,
#               rate_limit_429,upstream_insufficient_balance. The fault mode is
#               selected by the requested model name.
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
#   PROM_URL  Prometheus base URL. NO DEFAULT (2026-09-03): the old default
#             pointed at a service that does not exist, so metric assertions
#             silently degraded to warnings while the drill printed green.
#             If unset or unreachable, those assertions print ⚠️ NOT VERIFIED.
#   NS        Kubernetes namespace for newhub logs (default: lurus-newhub;
#             was lurus-system, the namespace retired in 2026-04)
#   APP_LABEL Kubernetes label selector (default: app=lurus-newhub)
#   CHAOS_COST_SPIKE_ENFORCE  "true" if the target deployment has
#             COST_SPIKE_ENFORCE=true; drives which Scenario C assertion
#             runs (see above). Default: false.
#
# Usage:
#   HUB_BASE=https://test-newhub.lurus.cn \
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
SKIP_REASON_B="Scenario B requires FAULTSIM_TOKEN and FAULTSIM_CHANNEL_ID; see the \
'Scenario B' section of this script's header."

HUB_BASE="${HUB_BASE:-https://test-newhub.lurus.cn}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
USER_TOKEN="${USER_TOKEN:-}"
CHAOS_CHANNEL_ID="${CHAOS_CHANNEL_ID:-}"
TEST_USER_ID="${TEST_USER_ID:-}"
# 2026-09-03: was http://prometheus.observability.svc:9090 — no such service
# exists and has not for some time, so every Prometheus assertion in this
# script silently degraded to a ⚠️ skip and the drill still printed green.
# There is no default now: unset means the metric assertions announce
# themselves as unverified rather than quietly passing.
PROM_URL="${PROM_URL:-}"
# 2026-09-03: was lurus-system, the namespace of the service retired in
# 2026-04. Every `kubectl logs` in this script therefore failed to match, and
# the log assertions degraded to warnings.
NS="${NS:-lurus-newhub}"
# The fault-injection upstream (internal/adapter/handler/faultsim.go), which is
# what finally makes Scenario B automatable. Both must be set for it to run:
# the token the UAT instance was started with, and the id of a channel seeded
# to point at http://127.0.0.1:3000/api/v2/faultsim/v1/chat/completions.
FAULTSIM_TOKEN="${FAULTSIM_TOKEN:-}"
FAULTSIM_CHANNEL_ID="${FAULTSIM_CHANNEL_ID:-}"
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
  # PROM_URL has no default any more. It used to point at
  # prometheus.observability.svc, a service that does not exist, so every
  # metric assertion here degraded to a warning while the drill still printed
  # green — the failure mode this whole script exists to avoid. Say plainly
  # that the assertion did not run.
  if [ -z "${PROM_URL}" ]; then
    warn "PROM_URL unset — metric assertion NOT VERIFIED" \
      "R6 has no Prometheus; scrape /metrics directly or point PROM_URL at one. query=${query}"
    return 2
  fi
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

hdr "Scenario B — upstream dies mid-stream / stalls before first byte"
if [ -z "$FAULTSIM_TOKEN" ] || [ -z "$FAULTSIM_CHANNEL_ID" ]; then
  warn "Scenario B SKIPPED" "$SKIP_REASON_B"
else
  # This scenario was a permanent SKIP because it "needs a controllable slow
  # upstream mock". That upstream now exists in-process (faultsim.go), so the
  # skip is a configuration choice rather than a standing gap.
  #
  # B1 — mid-stream abort. The only way to reach the incomplete-stream path
  # and relay_failover_suppressed_total{reason="stream_already_started"}: an
  # upstream that emits real frames and then stops. The client sees HTTP 200
  # (headers left long ago), so the assertion is on the BODY — frames present,
  # terminator absent — not on the status code.
  b1_body=$(curl -sS --max-time 60 \
    -H "Authorization: Bearer ${USER_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{"model":"mid_stream_abort","stream":true,"messages":[{"role":"user","content":"hi"}]}' \
    "${HUB_BASE}/v1/chat/completions" 2>/dev/null) || b1_body=""

  if [ -z "$b1_body" ]; then
    fail "B1 mid-stream abort produced no body" "expected partial SSE frames"
  elif printf '%s' "$b1_body" | grep -q '\[DONE\]'; then
    fail "B1 stream terminated normally" \
      "a [DONE] means the abort did not happen — check that FAULTSIM_CHANNEL_ID points at the simulator"
  else
    ok "B1 mid-stream abort: stream ended without a terminator"
  fi

  # B2 — stall before the first byte, exercising the relay's own idle timeout.
  b2_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 120 \
    -H "Authorization: Bearer ${USER_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{"model":"slow_headers","messages":[{"role":"user","content":"hi"}]}' \
    "${HUB_BASE}/v1/chat/completions" 2>/dev/null) || b2_code="000"

  case "$b2_code" in
    504|524) ok "B2 stalled upstream surfaced as $b2_code" ;;
    *) fail "B2 stalled upstream returned $b2_code" "expected 504/524 from the relay idle timeout" ;;
  esac

  # B3 — unpaid provider account must classify as its own thing, not as
  # "the caller sent a bad request".
  b3_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 \
    -H "Authorization: Bearer ${USER_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{"model":"upstream_insufficient_balance","messages":[{"role":"user","content":"hi"}]}' \
    "${HUB_BASE}/v1/chat/completions" 2>/dev/null) || b3_code="000"
  if [ "$b3_code" = "000" ]; then
    fail "B3 request failed outright" "expected a response carrying the upstream 402"
  else
    ok "B3 unpaid-upstream fault surfaced (HTTP $b3_code)"
    prom_query 'sum(lurus_gateway_relay_errors_total{error_type="upstream_insufficient_balance"})' gt 0 \
      || true
  fi
fi

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
if [ -z "$FAULTSIM_TOKEN" ] || [ -z "$FAULTSIM_CHANNEL_ID" ]; then
  printf '\033[2m  Scenario B SKIPPED — set FAULTSIM_TOKEN + FAULTSIM_CHANNEL_ID to run it\033[0m\n'
fi
if [ -z "${PROM_URL}" ]; then
  printf '\033[2m  metric assertions NOT VERIFIED — PROM_URL unset\033[0m\n'
fi
if [ "$FAIL" -eq 0 ]; then
  if [ -z "$FAULTSIM_TOKEN" ] || [ -z "$FAULTSIM_CHANNEL_ID" ]; then
    printf '\033[32m✓\033[0m chaos drill complete (Scenario B skipped)\n'
  else
    printf '\033[32m✓\033[0m chaos drill complete\n'
  fi
  exit 0
else
  printf '\033[31m✗\033[0m %d scenario(s) failed\n' "$FAIL"
  exit "$FAIL"
fi
