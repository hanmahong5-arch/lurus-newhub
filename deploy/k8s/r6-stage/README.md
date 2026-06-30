# lurus-newhub — R6 STAGE manifest

Live deployment of `2b-svc-newhub` on the R6 STAGE cluster, fronted by R6 host nginx.

- Cluster: R6 (`43.226.45.87`, Tailscale `100.122.83.20`), single-node K3s — per `lurus/CLAUDE.md` Server Landing SSOT
- Namespace: `lurus-staging` (PG `pg-access-control` netpol whitelists this ns, not `lurus-newhub` — see runbook Infra-1, 2026-06-13)
- Domain: https://test-newhub.lurus.cn
- Service: NodePort 30850 -> container port 3000
- Image: `ghcr.io/hanmahong5-arch/lurus-newhub:main` (floating tag, `imagePullPolicy: Always`)

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

Both deploy Deployment/Service **`lurus-newhub`** into ns **`lurus-staging`**. Use
**r6-stage/** for the host-nginx + platform-identity path validated in the Wave-1
runbook; use **staging/** for the Traefik + OIDC path. A full merge/delete is
**deferred** until the OIDC-vs-platform-identity choice is settled (ADR pending) —
see the Wave-2 deferred list.

## First apply

```bash
# 1. Seed the secret with real values (NEVER commit them):
kubectl -n lurus-staging create secret generic lurus-newhub-secrets \
  --from-literal=SESSION_SECRET='<real>' \
  --from-literal=SQL_DSN='<real>' \
  --from-literal=IDENTITY_SERVICE_INTERNAL_KEY='<real>' \
  --from-literal=IDENTITY_SESSION_SECRET='<real>'

# 2. Apply the rest (kustomize will try to apply secret-template.yaml too;
#    it is harmless because the keys above already exist — stringData is merged).
kubectl apply -k deploy/k8s/r6-stage/

# 3. Seed the default tenant (slug='lurus') — required for v2 multi-tenant
#    routes; without it /api/v2/lurus/* returns 404 "record not found".
#    db name is `newhub` (owner-confirmed 2026-06-14); `lurus_api` in the service
#    CLAUDE.md is the *schema* inside it, not the database. The PG pod is lurus-pg-1
#    in ns `database` (live-verified 2026-06-13).
ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-1 -- \
  psql -U lurus -d newhub" < deploy/k8s/r6-stage/seed-default-tenant.sql
```

## Sync nginx vhost to R6 host

```bash
scp deploy/r6-host-nginx/test-newhub.conf root@100.122.83.20:/etc/nginx/sites-available/test-newhub
ssh root@100.122.83.20 "ln -sf ../sites-available/test-newhub /etc/nginx/sites-enabled/ && nginx -t && systemctl reload nginx"
```

> Promote to PROD only after pinning the image to `main-<sha7>`.
