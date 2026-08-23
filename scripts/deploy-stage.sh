#!/usr/bin/env bash
# scripts/deploy-stage.sh — SSH-based STAGE deploy for lurus-newhub.
#
# STATUS (2026-08-23): this script is now the FALLBACK path. The primary
# convergence mechanism is the ArgoCD Application in deploy/k8s/argocd/
# (automated + selfHeal, tracks deploy/k8s/r6-stage on main) — see
# doc/decisions/2026-08-23-deploy-canonical-r6-stage.md. Use this script when
# ArgoCD itself is unreachable/misconfigured, or to rotate a secret (ArgoCD's
# sync deliberately excludes the Secret — see deploy/k8s/argocd/application.yaml
# prune:false rationale).
#
# WHY SSH (not GitHub Actions): the STAGE cluster API is Tailscale-only, so a
# GitHub-hosted runner cannot reach it. This script codifies the proven SSH
# path validated in the Wave-1 runbook. See doc/runbook/staging-deploy.md for
# the full narrative.
#
# Usage:
#   # Provide secrets via a sourced, NEVER-committed env file (recommended):
#   set -a; source ./stage-secrets.env; set +a    # add stage-secrets.env to .gitignore
#   bash scripts/deploy-stage.sh
#   # ...or inline (note: inline values land in your shell history):
#   SQL_DSN=... SESSION_SECRET=... ... bash scripts/deploy-stage.sh
#   # ...or skip the secret step entirely for a routine deploy (manifest-only —
#   # ArgoCD does this already; use this mode to hand-trigger the same thing
#   # without re-supplying all 5 secrets, e.g. when ArgoCD is down):
#   SKIP_SECRETS=1 bash scripts/deploy-stage.sh
#
# Required secrets (no safe default — the script fails fast if any is unset),
# UNLESS SKIP_SECRETS=1, in which case the secret upsert step (and its 5 env
# var checks) is skipped entirely and the existing in-cluster Secret is left
# untouched:
#   SQL_DSN  SESSION_SECRET  IDENTITY_SERVICE_INTERNAL_KEY  IDENTITY_SESSION_SECRET
#   LURUS_WHITELABEL_MASTER_SECRET
#
# Overrides (env):
#   SSH_HOST      default root@100.122.83.20 — R6 STAGE node itself (Tailscale); root's
#                 bare kubectl is k3s-configured, so no KUBECONFIG export is needed.
#                 (The old default root@100.98.57.55 is a DIFFERENT host, not R6.)
#   NAMESPACE     default lurus-newhub        — the ns the live serving Deployment/Service
#                 actually runs in. (The `database` PG netpol that once forced
#                 lurus-staging is gone — `kubectl get netpol -n database` is empty.)
#   OVERLAY       default deploy/k8s/r6-stage — the only overlay (deploy/k8s/staging/
#                 was retired 2026-08-23, see doc/decisions/2026-08-23-deploy-canonical-r6-stage.md).
#   SECRET_NAME   default lurus-newhub-secrets
#   SKIP_SECRETS  default unset — set to 1 to skip the secret env var checks and
#                 the secret upsert step (routine redeploy; only apply overlay +
#                 rollout + health check). Full mode (default) is required for
#                 secret rotation.
#   HEALTH_URL    default https://test-newhub.lurus.cn/api/health
#
# Idempotent: re-running with the same inputs converges to the same state
# (secret upsert via create|apply; kubectl apply is declarative).
#
# Requires locally: ssh, kubectl (used ONLY for offline `kubectl kustomize`
# rendering — the cluster itself is reached over SSH, never directly), curl.

set -euo pipefail

SSH_HOST="${SSH_HOST:-root@100.122.83.20}"
NAMESPACE="${NAMESPACE:-lurus-newhub}"
DEPLOYMENT="lurus-newhub"
OVERLAY="${OVERLAY:-deploy/k8s/r6-stage}"
SECRET_NAME="${SECRET_NAME:-lurus-newhub-secrets}"
SKIP_SECRETS="${SKIP_SECRETS:-}"
HEALTH_URL="${HEALTH_URL:-https://test-newhub.lurus.cn/api/health}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-300s}"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }
err() { log "ERROR: $*" >&2; exit 1; }

# 1. Require owner-provided secrets (fail fast — never invent values), unless
#    SKIP_SECRETS=1 (routine redeploy that leaves the existing Secret alone).
if [ -z "$SKIP_SECRETS" ]; then
  : "${SQL_DSN:?set SQL_DSN (postgres:// DSN); source a never-committed env file, or set SKIP_SECRETS=1}"
  : "${SESSION_SECRET:?set SESSION_SECRET (or SKIP_SECRETS=1)}"
  : "${IDENTITY_SERVICE_INTERNAL_KEY:?set IDENTITY_SERVICE_INTERNAL_KEY (or SKIP_SECRETS=1)}"
  : "${IDENTITY_SESSION_SECRET:?set IDENTITY_SESSION_SECRET (or SKIP_SECRETS=1)}"
  : "${LURUS_WHITELABEL_MASTER_SECRET:?set LURUS_WHITELABEL_MASTER_SECRET (or SKIP_SECRETS=1)}"
fi

command -v ssh     >/dev/null || err "ssh not found on PATH"
command -v kubectl >/dev/null || err "kubectl not found on PATH (needed for offline kustomize render)"
command -v curl    >/dev/null || err "curl not found on PATH"
[ -d "$OVERLAY" ]  || err "overlay dir not found: $OVERLAY (run from the repo root)"

log "STAGE deploy → host=$SSH_HOST ns=$NAMESPACE overlay=$OVERLAY skip_secrets=${SKIP_SECRETS:-0}"

# printf is a bash builtin, so secret values never appear in any process argv.
b64() { printf '%s' "$1" | base64 -w0; }

# 2. Ensure namespace, then (unless SKIP_SECRETS=1) idempotently upsert the
#    secret. The manifest is piped over SSH stdin to `kubectl apply -f -`, so
#    secret values never touch argv / the remote process table (only their
#    base64 form, on stdin).
log "Ensuring namespace $NAMESPACE ..."
ssh "$SSH_HOST" "kubectl create namespace '$NAMESPACE' --dry-run=client -o yaml | kubectl apply -f -" >/dev/null

if [ -z "$SKIP_SECRETS" ]; then
  log "Upserting secret $SECRET_NAME (values via stdin, not argv) ..."
  cat <<EOF | ssh "$SSH_HOST" "kubectl -n '$NAMESPACE' apply -f -" >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: $SECRET_NAME
type: Opaque
data:
  SQL_DSN: $(b64 "$SQL_DSN")
  SESSION_SECRET: $(b64 "$SESSION_SECRET")
  IDENTITY_SERVICE_INTERNAL_KEY: $(b64 "$IDENTITY_SERVICE_INTERNAL_KEY")
  IDENTITY_SESSION_SECRET: $(b64 "$IDENTITY_SESSION_SECRET")
  LURUS_WHITELABEL_MASTER_SECRET: $(b64 "$LURUS_WHITELABEL_MASTER_SECRET")
EOF
  log "Secret applied."
else
  log "SKIP_SECRETS=1 — leaving secret $SECRET_NAME untouched."
fi

# 3. Render the overlay locally (offline) and apply it on the cluster over SSH.
#    Rendering locally means the repo does not need to be checked out on the host.
log "Applying overlay $OVERLAY ..."
kubectl kustomize "$OVERLAY" | ssh "$SSH_HOST" "kubectl -n '$NAMESPACE' apply -f -"

# 4. Wait for the rollout, then verify deep health.
log "Waiting for rollout (timeout $WAIT_TIMEOUT) ..."
ssh "$SSH_HOST" "kubectl -n '$NAMESPACE' rollout status deployment/'$DEPLOYMENT' --timeout='$WAIT_TIMEOUT'" \
  || err "rollout did not reach Ready within $WAIT_TIMEOUT"

log "Health check: $HEALTH_URL (deep /api/health — 200 healthy, 503 degraded) ..."
for attempt in 1 2 3 4 5 6; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$HEALTH_URL" || echo 000)
  if [ "$code" = "200" ]; then
    log "Health OK (200). STAGE deploy complete."
    ssh "$SSH_HOST" "kubectl -n '$NAMESPACE' get deployment '$DEPLOYMENT' \
      -o jsonpath='{.spec.template.spec.containers[0].image}'" 2>/dev/null \
      && echo && exit 0
    exit 0
  fi
  log "health attempt $attempt/6 got HTTP $code, retrying in 5s ..."
  sleep 5
done
err "health check failed: $HEALTH_URL never returned 200 (check pod logs + DB reachability)"
