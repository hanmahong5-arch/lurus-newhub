# Runbook — STAGE deploy

- **Source:** operator (deploy / re-deploy of newhub to R6 STAGE)
- **Triggered by:** need to ship a build to R6 STAGE (`hub.lurus.cn`; `test-newhub.lurus.cn` is the separate UAT instance since 2026-08-30)
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
curl -fsS https://hub.lurus.cn/api/health | jq .
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

`SSH_HOST` is passed to `ssh` verbatim (no port flag), so when the Tailscale
path is down point it at an `~/.ssh/config` alias for the direct port:
`Host r6-direct` / `HostName 43.226.45.87` / `Port 12222` / `User root`, then
`SSH_HOST=r6-direct`. The UAT overlay is the same script with
`OVERLAY=deploy/k8s/r6-uat NAMESPACE=lurus-newhub-uat HEALTH_URL=https://test-newhub.lurus.cn/api/health`.
Both overlays' kustomizations exclude the Secret, so `apply` never touches it.

### When the node cannot pull the image (GitHub CDN unreachable)

Seen 2026-09-03: ArgoCD flipped to `Sync=Unknown` (repo-server's GitHub fetch
through the host `xray` `gh-egress` outbound timed out) and, at the same time,
`kubelet` could not pull the pinned digest — every blob request to
`pkg-containers.githubusercontent.com` ended in `EOF`, a 2 KB config blob
included, so it is a reachability cut, not size throttling. The domestic GHCR
mirrors did not finish either, and a `crane pull` from a workstation died with
an HTTP/2 `PROTOCOL_ERROR` after ten minutes. What worked, end to end:

```bash
# 0. The pinned digest must be a single OCI manifest and the package public:
crane manifest ghcr.io/hanmahong5-arch/lurus-newhub@sha256:<digest> | head -3
#    (crane: go run github.com/google/go-containerregistry/cmd/crane@latest ...)

# 1. Workstation: pull the OCI layout with HTTP/2 disabled (the digest is kept
#    verbatim; the layout is a directory, not a tar).
GODEBUG=http2client=0 crane pull --format=oci \
  ghcr.io/hanmahong5-arch/lurus-newhub@sha256:<digest> newhub-oci
tar -C newhub-oci -cf newhub.oci.tar .

# 2. Ship it to the node — /data is the only writable disk on R6.
scp newhub.oci.tar r6-direct:/data/tmp/newhub.oci.tar

# 3. Import into k3s's containerd (k3s ctr, NOT /usr/bin/ctr — that one talks
#    to dockerd's containerd). --digests --base-name names the image exactly
#    as the manifest references it, so imagePullPolicy=IfNotPresent hits.
ssh r6-direct 'k3s ctr -n k8s.io images import --digests \
  --base-name ghcr.io/hanmahong5-arch/lurus-newhub /data/tmp/newhub.oci.tar \
  && k3s ctr -n k8s.io images ls | grep <digest-prefix> \
  && rm /data/tmp/newhub.oci.tar'

# 4. The ImagePullBackOff pod picks the image up on its next back-off retry
#    (≤5 min); rollout completes on its own. Then verify:
ssh r6-direct 'kubectl -n lurus-newhub rollout status deployment/lurus-newhub; \
  curl -s http://127.0.0.1:30850/api/status | grep -o "\"version\":\"[^\"]*\""'
```

Every push to `main` that touches build inputs makes a new digest, so while
the CDN is cut each such merge needs this once more. Documentation-only
merges do not: since 2026-09-04 the publish workflow carries a `paths-ignore`
for `doc/**`, `docs/**`, `_bmad-output/**`, `**/*.md` and `deploy/**`
(verified on the first docs-only merge after it landed — that push produced a
Secret Scan run and no image build). When ArgoCD
recovers it compares live against git and finds them equal — no extra roll.

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

### Fast rollback while the image CDN is degraded

Measured 2026-09-03: R6's link to the GitHub image CDN runs at **10–17 KB/s**,
so a 28 MB layer never finishes and any rollback that needs a *new* pull is not
a rollback — it is an ImagePullBackOff. The path below deliberately needs no
pull at all, and it is the one to reach for first during an incident.

It works because of three properties that hold today:

- `imagePullPolicy: IfNotPresent` (`deploy/k8s/r6-stage/deployment.yaml:57`),
- the digest you are rolling *back to* already ran on this node, so it is in
  the local containerd store, and
- `maxUnavailable: 0` (line 20), so a pod that cannot start never takes the
  serving pod down with it.

```bash
# 1. find the digest that was live before the bad one. Auto-pin commits are
#    the deployment history; the previous one names a digest already on the node.
git log --oneline --grep='auto-pin' -5
git show <previous-auto-pin-sha>:deploy/k8s/r6-stage/deployment.yaml | grep -A1 '# pin:'

# 2. rewrite BOTH manifests to that digest (prod + UAT track the same one) and
#    push straight to main. [skip ci] is REQUIRED: without it the push builds a
#    new image and auto-pins a new digest, i.e. it undoes the rollback and asks
#    the CDN for 28 MB it cannot deliver.
git commit -am 'chore(deploy): roll back r6-stage to <version> [skip ci]'
git push origin main

# 3. ArgoCD converges on its next sync; kubelet finds the digest locally.
ssh root@100.122.83.20 "kubectl -n lurus-newhub rollout status deployment/lurus-newhub --timeout=180s"
curl -fsS https://hub.lurus.cn/api/status
```

Do NOT `kubectl set image` here: selfHeal puts the git-pinned digest back, so
the fix silently disappears at the next sync. Git is the only durable lever.

When the digest you need is genuinely absent from the node (e.g. rolling back
past a node rebuild), there is no way around moving the bytes — use the
side-channel transfer recipe (`crane pull` on a machine with bandwidth → `scp`
→ `k3s ctr import --digests`) rather than waiting on the CDN.

### Emergency rollback with sync paused

Faster still, but ArgoCD will undo it unless you pause sync first — selfHeal
reverts any manual `kubectl set image`/`rollout undo` back to the git-pinned
digest:

```bash
# 1. pause automated sync
kubectl -n argocd patch application lurus-newhub --type merge \
  -p '{"spec":{"syncPolicy":null}}'
# 2. roll back the live Deployment (correct namespace!)
ssh root@100.122.83.20 "kubectl -n lurus-newhub rollout undo deployment/lurus-newhub && \
  kubectl -n lurus-newhub rollout status deployment/lurus-newhub --timeout=120s"
# 3. verify, then land the matching git revert and re-enable automated sync by
#    re-applying deploy/k8s/argocd/application.yaml
curl -fsS https://hub.lurus.cn/api/status
```

## Verify

```bash
# deep health (200 healthy / 503 degraded). NB: r6-stage serves hub.lurus.cn;
# test-newhub.lurus.cn has pointed at the isolated UAT instance (:30851) since
# 2026-08-30, so verifying there proves nothing about this deployment.
curl -fsS https://hub.lurus.cn/api/health | jq .
# liveness (DB-free):
curl -fsS https://hub.lurus.cn/api/status
# active image:
ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub \
  -o jsonpath='{.spec.template.spec.containers[0].image}'"
```

## Notes / verify-before-trust

- `100.122.83.20` is R6's Tailscale IP (`lurus/CLAUDE.md` Server Landing
  SSOT). Older docs reference `100.98.57.55` — that is a different host, not
  R6; if it doesn't reach kubectl, use the Tailscale IP above.
- The seed DB name is `newhub` (owner-confirmed 2026-06-14). Its tables are in the
  **`public`** schema (measured 2026-08-24, 40 tables) — the `lurus_api` schema
  that the service CLAUDE.md used to name does not exist; that claim is corrected.
