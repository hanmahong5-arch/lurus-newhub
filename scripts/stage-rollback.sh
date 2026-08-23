#!/usr/bin/env bash
# scripts/stage-rollback.sh — STAGE rollback/restore drill for lurus-newhub
#
# Usage:
#   bash scripts/stage-rollback.sh            # roll back to previous image
#   bash scripts/stage-rollback.sh --restore  # restore forward (undo the rollback)
#
# Mechanism:
#   rollback: kubectl rollout undo deployment/lurus-newhub -n lurus-staging
#             (Kubernetes keeps the previous ReplicaSet; undo swaps back to it)
#   restore:  kubectl rollout undo deployment/lurus-newhub -n lurus-staging
#             (calling undo again steps forward in revision history)
#
# Idempotency: running the script twice with no change in state between runs is
# safe — rollout undo is effectively a no-op if the deployment is already on the
# target revision, and the health check is always performed regardless.
#
# Exit codes:
#   0  — rollout completed + health check passed
#   1  — kubectl error or health check failure (with descriptive message)

set -euo pipefail

# The live workload runs in ns lurus-newhub (r6-stage overlay; the old
# lurus-staging namespace never existed on R6 — the pg-access-control netpol
# that once forced it is gone, verified 2026-08-23). Overridable for drills.
# NOTE: with the ArgoCD Application synced (automated + selfHeal), a rollout
# undo is reverted by the next sync — pause/delete the Application first, or
# prefer `git revert` of the auto-pin commit (see doc/runbook/staging-deploy.md).
NAMESPACE="${NAMESPACE:-lurus-newhub}"
readonly DEPLOYMENT="lurus-newhub"
readonly HEALTH_URL="https://test-newhub.lurus.cn/api/status"
readonly WAIT_TIMEOUT="120s"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }
err() { log "ERROR: $*" >&2; exit 1; }

# Require kubectl on PATH
if ! command -v kubectl &>/dev/null; then
  err "kubectl not found on PATH"
fi

ACTION="${1:-rollback}"

case "$ACTION" in
  --restore)
    log "Action: RESTORE (rolling forward to next revision via undo)"
    ;;
  rollback|"")
    log "Action: ROLLBACK (rolling back to previous revision via undo)"
    ;;
  *)
    echo "Usage: $0 [--restore]" >&2
    exit 1
    ;;
esac

# Both rollback and restore use 'rollout undo' — Kubernetes revision history
# treats each undo as moving one step in the opposite direction.
log "Running: kubectl rollout undo deployment/$DEPLOYMENT -n $NAMESPACE"
if ! kubectl -n "$NAMESPACE" rollout undo "deployment/$DEPLOYMENT"; then
  err "kubectl rollout undo failed (check KUBECONFIG and cluster connectivity)"
fi

log "Waiting for rollout to complete (timeout: $WAIT_TIMEOUT)..."
if ! kubectl -n "$NAMESPACE" rollout status "deployment/$DEPLOYMENT" \
    --timeout="$WAIT_TIMEOUT"; then
  log "Rollout did not reach Ready in $WAIT_TIMEOUT — fetching pod state for diagnostics:"
  kubectl -n "$NAMESPACE" get pods -l "app=$DEPLOYMENT" || true
  err "deployment did not reach Ready within $WAIT_TIMEOUT"
fi

log "Rollout complete. Running health check against $HEALTH_URL ..."
# Retry up to 6 times (30 s total) — pod may need a few seconds to register
# with the Ingress after the rollout reports Ready.
HEALTH_OK=0
for attempt in 1 2 3 4 5 6; do
  if curl -sf --max-time 10 "$HEALTH_URL" > /dev/null 2>&1; then
    HEALTH_OK=1
    break
  fi
  log "Health check attempt $attempt/6 failed, retrying in 5s..."
  sleep 5
done

if [[ "$HEALTH_OK" -eq 0 ]]; then
  log "Current pod list for diagnostics:"
  kubectl -n "$NAMESPACE" get pods -l "app=$DEPLOYMENT" || true
  err "Health check failed after 6 attempts: $HEALTH_URL did not return 2xx"
fi

log "Health check passed. $ACTION complete."

# Emit the active image tag so the operator can confirm which revision is live.
log "Active image:"
kubectl -n "$NAMESPACE" get deployment "$DEPLOYMENT" \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null && echo
