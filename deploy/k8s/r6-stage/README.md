# lurus-newhub — R6 STAGE manifest

Live deployment of `2b-svc-newhub` on the R6 STAGE cluster, fronted by R6 host nginx.

- Cluster: R6 (`43.226.45.87`, Tailscale `100.122.83.20`), single-node K3s — per `lurus/CLAUDE.md` Server Landing SSOT
- Namespace: `lurus-newhub` — the ns the live Deployment/Service actually run in. (An earlier revision moved this to `lurus-staging` believing PG's `pg-access-control` netpol required it; that netpol no longer exists — `kubectl get netpol -n database` is empty — so the manifests were reverted to `lurus-newhub` to match the live cluster, 2026-07-07.)
- Domain: https://test-newhub.lurus.cn
- Service: NodePort 30850 -> container port 3000
- Image: `ghcr.io/hanmahong5-arch/lurus-newhub@sha256:c5ab7938…` (pinned digest, `imagePullPolicy: IfNotPresent`) — reconciled 2026-08-15 to the live cluster digest (= tag `main-20260811-d12acb2`, built from `main@d12acb24`). Roll forward by re-resolving `:main`, updating `deployment.yaml`, and syncing the ArgoCD app (below); rolling the cluster without bumping this file recreates the drift this reconcile just closed.
- GitOps: ArgoCD Application `lurus-newhub-r6-stage` (ns `argocd`) tracks this directory with **manual** sync — it reports Synced/OutOfSync but never mutates the cluster on its own. See `argocd-application.yaml` for the registration rationale and the removal one-liner.

## Which overlay when (staging/ vs r6-stage/)

There are two STAGE overlays and they are **NOT** interchangeable — they differ in
**auth integration mode**, which is a product decision, so do not merge them blindly:

| | `deploy/k8s/staging/` | `deploy/k8s/r6-stage/` (this dir) |
|---|---|---|
| Auth | OIDC (`OIDC_ENABLED=true`) | platform-identity (`OIDC_ENABLED=false` + `IDENTITY_*`) |
| Image tag | `:staging` (CI-built) | `:main` (manual) |
| Ingress | Traefik `IngressRoute`, ClusterIP | host-nginx → NodePort 30850 |
| Replicas | 1 | 3 |
| Secret | `lurus-newhub-staging-secrets` | `lurus-newhub-secrets` |

**r6-stage/** deploys Deployment/Service **`lurus-newhub`** into ns **`lurus-newhub`** (the live target); the legacy **staging/** overlay still names ns `lurus-staging` and has zero cluster footprint. Use
**r6-stage/** for the host-nginx + platform-identity path validated in the Wave-1
runbook; use **staging/** for the Traefik + OIDC path. A full merge/delete is
**deferred** until the OIDC-vs-platform-identity choice is settled (ADR pending) —
see the Wave-2 deferred list.

## First apply

```bash
# 1. Seed the secret with real values (NEVER commit them):
kubectl -n lurus-newhub create secret generic lurus-newhub-secrets \
  --from-literal=SESSION_SECRET='<real>' \
  --from-literal=SQL_DSN='<real>' \
  --from-literal=IDENTITY_SERVICE_INTERNAL_KEY='<real>' \
  --from-literal=IDENTITY_SESSION_SECRET='<real>'

# 2. Apply the rest (kustomize will try to apply secret-template.yaml too;
#    it is harmless because the keys above already exist — stringData is merged).
kubectl apply -k deploy/k8s/r6-stage/

# 3. (Optional/legacy) Seed the default tenant (slug='lurus') by hand — migration
#    021 §4 now self-seeds this idempotently on boot (resolves by id='default' OR
#    slug='lurus', creates only if truly absent), so this manual step is no longer
#    required for a fresh deploy. Keep it only for pre-021 bootstrap or recovery;
#    if run, the script now uses the canonical id='default' (021's compat branch
#    still tolerates a legacy id='lurus-default' row from old STAGE data, but new
#    seeds must not create more of it).
#    db name is `newhub` (owner-confirmed 2026-06-14); `lurus_api` in the service
#    CLAUDE.md is the *schema* inside it, not the database. The PG pod is lurus-pg-1
#    in ns `database` (live-verified 2026-06-13).
ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-0 -- \
  psql -U lurus -d newhub" < deploy/k8s/r6-stage/seed-default-tenant.sql
# (PG pod is lurus-pg-0 in ns database — live-verified 2026-07-07; a stale ref to
#  lurus-pg-1 was corrected. seed-default-tenant.sql already targets lurus-pg-0.)
```

## Sync nginx vhost to R6 host

```bash
scp deploy/r6-host-nginx/test-newhub.conf root@100.122.83.20:/etc/nginx/sites-available/test-newhub
ssh root@100.122.83.20 "ln -sf ../sites-available/test-newhub /etc/nginx/sites-enabled/ && nginx -t && systemctl reload nginx"
```

> Promote to PROD only after pinning the image to `main-<sha7>`.
