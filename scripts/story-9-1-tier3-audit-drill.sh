#!/usr/bin/env bash
# story-9-1-tier3-audit-drill.sh — automate the 3 SQL queries from
# _bmad-output/planning-artifacts/story-9-1-tier3-usage-audit.md
# against PROD/STAGE PG and write a dated markdown report.
#
# Called by stage-smoke.sh; also runnable standalone.
#
# Required env (one of):
#   PG_DSN   full libpq URL, e.g. postgres://user:pass@host:5432/newhub
#   R6_HOST  SSH target (e.g. 100.122.83.20, R6's Tailscale IP) — uses
#            kubectl exec into the PG pod (StatefulSet lurus-pg, pod
#            lurus-pg-0, ns database — doc/runbook/database.md)
#
# Exit codes:
#   0 — all 3 queries returned
#   1 — query failure / connection failure
#   2 — neither PG_DSN nor R6_HOST present

set -u
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if command -v git >/dev/null 2>&1; then
    GIT_ROOT=$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || echo "$REPO_ROOT")
    REPO_ROOT="$GIT_ROOT"
fi
SQL_FILE="$SCRIPT_DIR/e9-1-prod-usage.sql"
[ -f "$SQL_FILE" ] || { echo "[drill] FAIL: SQL file not found: $SQL_FILE" >&2; exit 1; }

REPORT_DIR="$REPO_ROOT/doc/reports"
mkdir -p "$REPORT_DIR"
DATE_TAG=$(date +%Y%m%d)
REPORT="$REPORT_DIR/e9-1-tier3-usage-${DATE_TAG}.md"

log() { echo "[9-1-drill] $*"; }

run_psql_local() {
    # PG_DSN path: run psql directly.
    log "running psql against PG_DSN (host masked)"
    psql "$PG_DSN" \
        --no-psqlrc \
        --pset='border=2' \
        --pset='linestyle=unicode' \
        -f "$SQL_FILE" 2>&1
}

run_psql_remote() {
    # R6_HOST path: SSH and kubectl exec into the PG pod, then pipe SQL in.
    # StatefulSet lurus-pg, pod lurus-pg-0, ns database — doc/runbook/database.md
    # (live-verified 2026-08-24; the ns used to be misdocumented as
    # lurus-system, which is newhub's own namespace, not PG's).
    log "running psql via ssh root@$R6_HOST → kubectl exec PG pod"
    # shellcheck disable=SC2087
    ssh -o BatchMode=yes "root@$R6_HOST" bash -s <<REMOTE_EOF < "$SQL_FILE"
set -e
POD="\${POSTGRES_POD:-lurus-pg-0}"
NS="\${POSTGRES_NS:-database}"
if ! kubectl -n "\$NS" get pod "\$POD" >/dev/null 2>&1; then
    echo "[remote] PG pod \$POD not found in ns \$NS" >&2
    exit 1
fi
DB="\${POSTGRES_DB:-newhub}"
USR="\${POSTGRES_USER:-postgres}"
# Feed SQL from stdin into kubectl exec → psql
kubectl -n "\$NS" exec -i "\$POD" -- psql -U "\$USR" -d "\$DB" --no-psqlrc 2>&1
REMOTE_EOF
}

# ── Choose transport ──────────────────────────────────────────────────────
if [ -n "${PG_DSN:-}" ]; then
    if ! command -v psql >/dev/null 2>&1; then
        log "FAIL: PG_DSN set but psql not installed locally"
        exit 1
    fi
    TRANSPORT="local"
elif [ -n "${R6_HOST:-}" ]; then
    if ! command -v ssh >/dev/null 2>&1; then
        log "FAIL: R6_HOST set but ssh not available"
        exit 1
    fi
    TRANSPORT="remote"
else
    log "FAIL: need either PG_DSN or R6_HOST"
    exit 2
fi

# ── Capture output ────────────────────────────────────────────────────────
TMP_OUT=$(mktemp -t e9-1-drill.XXXXXX 2>/dev/null || mktemp)
trap 'rm -f "$TMP_OUT"' EXIT

if [ "$TRANSPORT" = "local" ]; then
    run_psql_local > "$TMP_OUT"
    rc=$?
else
    run_psql_remote > "$TMP_OUT"
    rc=$?
fi

# ── Validate: must contain all 3 query markers ────────────────────────────
q1_present=$(grep -c "Query 1:" "$TMP_OUT" || true)
q2_present=$(grep -c "Query 2:" "$TMP_OUT" || true)
q3_present=$(grep -c "Query 3:" "$TMP_OUT" || true)

# ── Write report ──────────────────────────────────────────────────────────
{
    echo "# Story 9-1 — Tier-3 Modality Usage Audit"
    echo
    echo "**Run:** $(date -u +'%Y-%m-%dT%H:%M:%SZ')  "
    echo "**Transport:** $TRANSPORT  "
    echo "**SQL source:** \`scripts/e9-1-prod-usage.sql\`  "
    echo "**psql exit code:** $rc  "
    echo "**Query markers found:** Q1=$q1_present, Q2=$q2_present, Q3=$q3_present"
    echo
    echo "## Output"
    echo
    echo '```'
    cat "$TMP_OUT"
    echo '```'
    echo
    echo "## References"
    echo
    echo "- Audit doc: \`_bmad-output/planning-artifacts/story-9-1-tier3-usage-audit.md\`"
    echo "- Schema: \`internal/domain/entity/log.go\` (Log struct, type=2 = LogTypeConsume, model_name col)"
    echo "- Channel types: \`internal/pkg/constant/channel.go\` (2/5/36/50/51/52/54/55)"
} > "$REPORT"

log "report written: $REPORT"

# ── Exit logic ────────────────────────────────────────────────────────────
if [ "$rc" -ne 0 ]; then
    log "FAIL: psql exited $rc"
    exit 1
fi
if [ "${q1_present:-0}" -lt 1 ] || [ "${q2_present:-0}" -lt 1 ] || [ "${q3_present:-0}" -lt 1 ]; then
    log "FAIL: missing query markers (Q1=$q1_present Q2=$q2_present Q3=$q3_present)"
    exit 1
fi

log "PASS: all 3 queries returned"
exit 0
