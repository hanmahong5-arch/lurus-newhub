# Runbook — STAGE deploy

- **Source:** operator (deploy / re-deploy of newhub to R6 STAGE)
- **Triggered by:** need to ship a build to `test-newhub.lurus.cn`
- **Severity:** procedure
- **Last review:** 2026-08-23 — rewritten per `doc/decisions/2026-08-23-deploy-canonical-r6-stage.md`

## Primary path: ArgoCD (automatic)

Merging to `main` already triggers `bump_r6_manifest` (`docker-image-main.yml`),
which auto-commits the new image digest into
`deploy/k8s/r6-stage/deployment.yaml` (`[skip ci]`). Once
`deploy/k8s/argocd/application.yaml` is wired up (see
`deploy/k8s/argocd/README.md` for the one-time setup), ArgoCD's automated +
selfHeal sync notices that commit and converges the cluster **without any
further manual step**. This is now the default expectation for a STAGE
deploy — no SSH, no operator action.

Verify a deploy landed:

```bash
kubectl -n argocd get application lurus-newhub -o jsonpath='{.status.sync.status} {.status.health.status}{"\n"}'
curl -fsS https://test-newhub.lurus.cn/api/health | jq .
```

If the Application shows `OutOfSync`/`Unknown` for longer than one poll
interval, or repo credentials are not yet configured (see
`deploy/k8s/argocd/README.md`), fall back to the SSH path below.

### Why not GitHub Actions

`.github/workflows/deploy-staging.yml` (and the `deploy/k8s/staging/` overlay
it applied) was deleted 2026-08-23 — see the ADR above. The STAGE cluster API
is Tailscale-only, so a GitHub-hosted runner could never reach it; that job
never once deployed anything in its lifetime. It is not being kept around "for
its warning" — this ADR is now the durable record of the gap.

## Fallback path: SSH script

Use `scripts/deploy-stage.sh` when ArgoCD is unreachable/misconfigured, or to
rotate a secret (ArgoCD's sync deliberately never touches the Secret — ArgoCD
prune is off precisely so it won't delete it, and it isn't in the overlay's
resource list to begin with). This is the flow validated live on R6 during the
Wave-1 industrial-readiness campaign; idempotent, safe to re-run.

```bash
# 1. Put the real secrets in a NEVER-committed env file (add to .gitignore):
cat > stage-secrets.env <<'EOF'
export SQL_DSN='postgres://...'
export SESSION_SECRET='...'
export IDENTITY_SERVICE_INTERNAL_KEY='...'
export IDENTITY_SESSION_SECRET='...'
export LURUS_WHITELABEL_MASTER_SECRET='...'
EOF

# 2. Source it and run the deploy from the repo root:
set -a; source ./stage-secrets.env; set +a
bash scripts/deploy-stage.sh

# ...or for a routine redeploy that doesn't need to touch secrets at all
# (e.g. ArgoCD is down and you just need the manifest re-applied):
SKIP_SECRETS=1 bash scripts/deploy-stage.sh
```

What the script does:
1. `ssh root@100.122.83.20` ensure namespace `lurus-newhub` exists.
2. Unless `SKIP_SECRETS=1`: idempotently upsert secret `lurus-newhub-secrets`
   — values are piped over SSH **stdin** (base64) to `kubectl apply -f -`, so
   they never appear in any process argv on the local or remote host.
3. Render `deploy/k8s/r6-stage/` **locally** with `kubectl kustomize` and apply
   it over SSH (so the repo need not be checked out on the host).
4. `kubectl rollout status deployment/lurus-newhub` then poll the deep
   `/api/health` until `200`.

### Overrides

| env | default | note |
|---|---|---|
| `SSH_HOST` | `root@100.122.83.20` | R6 Tailscale IP |
| `NAMESPACE` | `lurus-newhub` | the ns the live Deployment/Service actually run in |
| `OVERLAY` | `deploy/k8s/r6-stage` | the only overlay (`deploy/k8s/staging/` retired 2026-08-23) |
| `SECRET_NAME` | `lurus-newhub-secrets` | |
| `SKIP_SECRETS` | unset | set to `1` to skip the secret checks/upsert entirely |

## Rollback

⚠️ **Do not use `scripts/stage-rollback.sh` as-is.** It hardcodes
`readonly NAMESPACE="lurus-staging"` (no env override) — a namespace this
cluster does not use; the workload is in ns `lurus-newhub` (`deploy/k8s/r6-stage/README.md`
2026-07-07 revert, live-re-verified 2026-08-22 in `doc/runbook/seam-s1-activation.md`).
Every run therefore dies at `kubectl rollout undo` with `namespaces
"lurus-staging" not found`. Fixing that script is tracked in the ADR's
残留缺口 section; until then use the commands below.

Preferred rollback = **git**, because that is what ArgoCD converges to:

```bash
# revert the auto-pin commit that shipped the bad digest, push to main;
# ArgoCD rolls the cluster back on the next sync (no SSH needed).
git log --oneline --grep='auto-pin' -5
git revert <bad-auto-pin-sha>        # then push per normal review flow
```

Emergency rollback (faster, but ArgoCD will undo it unless you pause sync
first — selfHeal reverts any manual `kubectl set image`/`rollout undo` back to
the git-pinned digest):

```bash
# 1. pause automated sync
kubectl -n argocd patch application lurus-newhub --type merge \
  -p '{"spec":{"syncPolicy":null}}'
# 2. roll back the live Deployment (correct namespace!)
ssh root@100.122.83.20 "kubectl -n lurus-newhub rollout undo deployment/lurus-newhub && \
  kubectl -n lurus-newhub rollout status deployment/lurus-newhub --timeout=120s"
# 3. verify, then land the matching git revert and re-enable automated sync by
#    re-applying deploy/k8s/argocd/application.yaml
curl -fsS https://test-newhub.lurus.cn/api/status
```

## Verify

```bash
# deep health (200 healthy / 503 degraded):
curl -fsS https://test-newhub.lurus.cn/api/health | jq .
# liveness (DB-free):
curl -fsS https://test-newhub.lurus.cn/api/status
# active image:
ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub \
  -o jsonpath='{.spec.template.spec.containers[0].image}'"
```

## Notes / verify-before-trust

- `100.122.83.20` is R6's Tailscale IP (`lurus/CLAUDE.md` Server Landing
  SSOT). Older docs reference `100.98.57.55` — that is a different host, not
  R6; if it doesn't reach kubectl, use the Tailscale IP above.
- The seed DB name is `newhub` (owner-confirmed 2026-06-14); `lurus_api` in the
  service CLAUDE.md is the *schema* inside that DB, not the database name.
