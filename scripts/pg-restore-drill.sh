#!/usr/bin/env bash
# pg-restore-drill.sh — Monthly automated test of wal-g restore.
#
# What it does:
#   1. Confirms WALG_S3_PREFIX is set (skips if backups are disabled).
#   2. Verifies at least one base backup exists.
#   3. Asserts RPO: newest base backup age <= RPO_TARGET_HOURS (default 24).
#   4. Spins up a throwaway PG container on a random local port.
#   5. Restores LATEST into the throwaway via wal-g backup-fetch.
#   6. Boots PG with WAL replay → must reach a writable state.
#   7. Runs a sanity SQL (count rows in expected tables).
#   8. Asserts RTO: total elapsed <= RTO_TARGET_SECONDS (default 300).
#   9. Tears down the throwaway and reports PASS/FAIL with timing.
#      With --keep-db, skips teardown so operator can inspect.
#
# Exit codes: 0 = pass, 1 = fail (with reason on stderr).
#
# Flags:
#   --quiet     suppress info logs (cron-friendly)
#   --keep-db   skip cleanup so operator can connect to restored DB
#
# Env overrides:
#   RPO_TARGET_HOURS     default 24
#   RTO_TARGET_SECONDS   default 300
#   ENV_FILE             default <repo-root>/deploy/single-node/.env
#
# Usage:
#   bash scripts/pg-restore-drill.sh                # one-off
#   bash scripts/pg-restore-drill.sh --quiet        # cron-friendly
#   bash scripts/pg-restore-drill.sh --keep-db      # post-mortem mode
#
# Cron (host):
#   0 4 1 * * cd /opt/lurus-hub && bash scripts/pg-restore-drill.sh --quiet \
#             >> /var/log/lurus-restore-drill.log 2>&1

set -eu

QUIET=0
KEEP_DB=0
for arg in "$@"; do
    case "$arg" in
        --quiet)   QUIET=1 ;;
        --keep-db) KEEP_DB=1 ;;
        *) echo "[drill] unknown flag: $arg" >&2; exit 2 ;;
    esac
done

log() { [ $QUIET -eq 0 ] && echo "[drill] $*" || true; }
fail() { echo "[drill] FAIL: $*" >&2; exit 1; }

start_ts=$(date +%s)

# ── SLA targets (env-overridable) ────────────────────────────────────────
RPO_TARGET_HOURS="${RPO_TARGET_HOURS:-24}"
RTO_TARGET_SECONDS="${RTO_TARGET_SECONDS:-300}"

# ── 0. Resolve repo-root-anchored env file ───────────────────────────────
# Anchor to the script's parent dir so cwd doesn't matter. Allow ENV_FILE
# override for callers that have a different layout.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if command -v git >/dev/null 2>&1; then
    GIT_ROOT=$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || echo "$REPO_ROOT")
    REPO_ROOT="$GIT_ROOT"
fi
ENV_FILE="${ENV_FILE:-$REPO_ROOT/deploy/single-node/.env}"

# ── 1. Source env ────────────────────────────────────────────────────────
[ -f "$ENV_FILE" ] || fail "env file not found: $ENV_FILE (override via ENV_FILE env var)"
# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a

if [ -z "${WALG_S3_PREFIX:-}" ]; then
    log "WALG_S3_PREFIX empty — backups disabled, skipping drill."
    exit 0
fi

IMAGE="${IMAGE:-lurus-postgres:walg-15}"
CONTAINER="lurus-pg-restore-drill-$$"
PORT=$((20000 + RANDOM % 10000))
VOL="restore-drill-$$"

cleanup() {
    if [ $KEEP_DB -eq 1 ]; then
        log "──── --keep-db: SKIPPING cleanup ────"
        log "  container: $CONTAINER"
        log "  volume:    $VOL"
        log "  port:      $PORT"
        log "  connect:   docker exec -it $CONTAINER psql -U ${POSTGRES_USER:-lurus} -d ${POSTGRES_DB:-newhub}"
        log "  cleanup:   docker rm -f $CONTAINER && docker volume rm $VOL"
        return
    fi
    log "cleanup: removing $CONTAINER + volume $VOL"
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
    docker volume rm "$VOL" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ── 2. Verify backup exists ──────────────────────────────────────────────
log "listing backups in $WALG_S3_PREFIX"
backup_list=$(docker run --rm \
    -e WALG_S3_PREFIX -e AWS_ENDPOINT="$WALG_AWS_ENDPOINT" \
    -e AWS_ACCESS_KEY_ID="$WALG_AWS_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$WALG_AWS_SECRET_ACCESS_KEY" \
    -e AWS_REGION="${WALG_AWS_REGION:-us-east-1}" \
    -e AWS_S3_FORCE_PATH_STYLE=true \
    "$IMAGE" wal-g backup-list 2>/dev/null || true)

backup_count=$(echo "$backup_list" | tail -n +2 | grep -c . || true)
if [ "${backup_count:-0}" -lt 1 ]; then
    fail "no base backups found in $WALG_S3_PREFIX (have you taken the first one?)"
fi
log "found $backup_count base backups"

# ── 2a. RPO check — newest base backup must be fresher than target ───────
# wal-g backup-list output format: "name  modified  wal_segment_backup_start ...".
# We use the modified timestamp of the last row (newest).
newest_line=$(echo "$backup_list" | tail -n +2 | tail -1)
newest_ts_str=$(echo "$newest_line" | awk '{print $2"T"$3}' | sed 's/Z$//')
if [ -n "${newest_ts_str:-}" ]; then
    # Try several parse styles; wal-g typically outputs "2026-05-19 03:14:22Z"
    newest_epoch=$(date -d "$newest_ts_str" +%s 2>/dev/null \
                   || date -d "${newest_ts_str%T*} ${newest_ts_str#*T}" +%s 2>/dev/null \
                   || echo "")
    if [ -n "$newest_epoch" ]; then
        now_epoch=$(date +%s)
        age_seconds=$(( now_epoch - newest_epoch ))
        age_hours=$(( age_seconds / 3600 ))
        log "newest base backup age: ${age_hours}h (target RPO: ${RPO_TARGET_HOURS}h)"
        if [ "$age_hours" -gt "$RPO_TARGET_HOURS" ]; then
            fail "RPO breach: latest backup is ${age_hours}h old, target is ${RPO_TARGET_HOURS}h"
        fi
    else
        log "WARN: could not parse newest backup timestamp '$newest_ts_str'; skipping RPO assertion"
    fi
else
    log "WARN: empty backup list timestamp; skipping RPO assertion"
fi

# ── 3. Create throwaway volume + restore LATEST ──────────────────────────
docker volume create "$VOL" >/dev/null
log "fetching LATEST base backup into $VOL"
docker run --rm \
    -e WALG_S3_PREFIX -e AWS_ENDPOINT="$WALG_AWS_ENDPOINT" \
    -e AWS_ACCESS_KEY_ID="$WALG_AWS_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$WALG_AWS_SECRET_ACCESS_KEY" \
    -e AWS_REGION="${WALG_AWS_REGION:-us-east-1}" \
    -e AWS_S3_FORCE_PATH_STYLE=true \
    -v "$VOL:/var/lib/postgresql/data" \
    "$IMAGE" wal-g backup-fetch /var/lib/postgresql/data LATEST

# ── 4. Configure WAL replay ─────────────────────────────────────────────
docker run --rm \
    -v "$VOL:/var/lib/postgresql/data" \
    "$IMAGE" bash -c '
        cat >> /var/lib/postgresql/data/postgresql.auto.conf <<EOF
restore_command = '"'"'/usr/local/bin/wal-g wal-fetch %f %p'"'"'
EOF
        touch /var/lib/postgresql/data/recovery.signal
        # PG complains if PGDATA is group-readable
        chmod 700 /var/lib/postgresql/data
    '

# ── 5. Boot the throwaway PG ─────────────────────────────────────────────
log "starting $CONTAINER on port $PORT"
docker run -d --name "$CONTAINER" \
    -e POSTGRES_USER="${POSTGRES_USER:-lurus}" \
    -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
    -e POSTGRES_DB="${POSTGRES_DB:-newhub}" \
    -e WALG_S3_PREFIX -e AWS_ENDPOINT="$WALG_AWS_ENDPOINT" \
    -e AWS_ACCESS_KEY_ID="$WALG_AWS_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$WALG_AWS_SECRET_ACCESS_KEY" \
    -e AWS_REGION="${WALG_AWS_REGION:-us-east-1}" \
    -e AWS_S3_FORCE_PATH_STYLE=true \
    -p "127.0.0.1:$PORT:5432" \
    -v "$VOL:/var/lib/postgresql/data" \
    "$IMAGE" >/dev/null

# Wait up to RTO_TARGET_SECONDS for PG to finish recovery + accept connections.
recovered=0
for i in $(seq 1 "$RTO_TARGET_SECONDS"); do
    if docker exec "$CONTAINER" pg_isready -U "${POSTGRES_USER:-lurus}" >/dev/null 2>&1; then
        in_recovery=$(docker exec "$CONTAINER" psql -U "${POSTGRES_USER:-lurus}" -d "${POSTGRES_DB:-newhub}" \
            -t -A -c "SELECT pg_is_in_recovery();" 2>/dev/null || echo "?")
        if [ "$in_recovery" = "f" ]; then
            log "PG ready, recovery complete (${i}s)"
            recovered=1
            break
        fi
    fi
    sleep 1
done
if [ "$recovered" -ne 1 ]; then
    fail "PG did not finish recovery within ${RTO_TARGET_SECONDS}s"
fi

# ── 6. Sanity SQL ────────────────────────────────────────────────────────
# Tables expected to exist on a healthy newhub DB. Adjust if schema changes.
EXPECTED_TABLES="users tokens channels logs tenants"
for t in $EXPECTED_TABLES; do
    if ! docker exec "$CONTAINER" psql -U "${POSTGRES_USER:-lurus}" -d "${POSTGRES_DB:-newhub}" \
            -t -A -c "SELECT 1 FROM information_schema.tables WHERE table_name='$t';" 2>/dev/null \
            | grep -q "^1$"; then
        fail "expected table '$t' missing in restored DB — backup is incomplete or schema drifted"
    fi
done
log "sanity check: all expected tables present ($EXPECTED_TABLES)"

# Row count snapshot (informational; not asserted — counts vary by deploy)
for t in $EXPECTED_TABLES; do
    count=$(docker exec "$CONTAINER" psql -U "${POSTGRES_USER:-lurus}" -d "${POSTGRES_DB:-newhub}" \
        -t -A -c "SELECT count(*) FROM $t;" 2>/dev/null)
    log "  $t: $count rows"
done

# ── 7. RTO assertion ─────────────────────────────────────────────────────
elapsed=$(( $(date +%s) - start_ts ))
if [ "$elapsed" -gt "$RTO_TARGET_SECONDS" ]; then
    fail "RTO breach: restore took ${elapsed}s, target is ${RTO_TARGET_SECONDS}s"
fi

echo "[drill] PASS: full restore + recovery + sanity SQL in ${elapsed}s (RTO target ${RTO_TARGET_SECONDS}s)"
exit 0
