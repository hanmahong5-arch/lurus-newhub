# Staging Environment Runbook

Pre-production env mirroring prod with reduced resources.

> **Reconciled 2026-06-13 (Wave-2 C2).** This runbook documents the
> `deploy/k8s/staging/` overlay (Zitadel auth, Traefik, `staging-api.lurus.cn`) —
> the *sibling* of the `r6-stage/` platform-identity overlay
> (`test-newhub.lurus.cn`); see `deploy/k8s/r6-stage/README.md` "Which overlay
> when". Names/images/secret/PG-pod below were corrected against the live
> manifests + cluster. The DB **name** is `newhub` (owner-confirmed 2026-06-14;
> `lurus_api` is the schema inside it). Items still tagged **⚠️VERIFY** below (PG
> host:port; whether this overlay gets its own isolated DB vs sharing r6-stage's
> `newhub`) were not live-confirmed — check against the live `SQL_DSN` before trusting.

| Property | Value |
|----------|-------|
| Namespace | `lurus-staging` |
| URL | https://staging-api.lurus.cn |
| Deployment / Service | `lurus-newhub` |
| Database | `newhub` (owner-confirmed 2026-06-14; schema `lurus_api`) — ⚠️VERIFY whether staging gets an isolated DB or shares r6-stage's |
| Redis DB | 1 (prod uses 0) |
| Replicas | 1 |
| Image | `ghcr.io/hanmahong5-arch/lurus-newhub:staging` |
| Secret | `lurus-newhub-staging-secrets` |
| Meilisearch | `staging_*` indexes |

## Setup

```bash
# 1. Staging DB (PG pod lurus-pg-1 in ns `database`, live-verified 2026-06-13).
#    newhub's DB name is `newhub` (owner-confirmed 2026-06-14). Only run CREATE
#    DATABASE if you want an isolated staging DB; if staging shares the r6-stage
#    `newhub` DB it already exists — point SQL_DSN at it and skip this step.
kubectl exec -it lurus-pg-1 -n database -- psql -U lurus -c "CREATE DATABASE newhub; GRANT ALL PRIVILEGES ON DATABASE newhub TO lurus;"

# 2. Secrets
kubectl -n lurus-staging create secret generic lurus-newhub-staging-secrets \
  --from-literal=SESSION_SECRET="$(openssl rand -hex 32)" \
  --from-literal=SQL_DSN='postgres://lurus:YOUR_PASSWORD@<PG_HOST>:<PORT>/newhub' \
  --from-literal=OIDC_CLIENT_ID='YOUR_STAGING_CLIENT_ID'
#  ⚠️VERIFY <PG_HOST>:<PORT> — older copy hard-coded 100.94.177.10:30543; the
#  in-cluster PG is lurus-pg-1.database.svc. Use whichever the live DSN uses.

# 3. Zitadel staging OIDC app "lurus-newhub-staging", redirect https://staging-api.lurus.cn/api/v2/oauth/callback → copy Client ID to secret

# 4. Deploy
kubectl apply -k deploy/k8s/staging/
kubectl -n lurus-staging get pods,svc,ingressroute

# 5. DNS: staging-api.lurus.cn  A  <K3s Ingress IP>
```

## Deploy

Auto-deploy on push/merge to `main` is **dead** — the cluster API is Tailscale-only
so the GitHub runner can't reach it (`.github/workflows/deploy-staging.yml` `deploy`
job skips). Use the SSH path: `scripts/deploy-stage.sh` (see
`doc/runbook/staging-deploy.md`). Manual equivalent:

```bash
docker build -t ghcr.io/hanmahong5-arch/lurus-newhub:staging . && docker push ghcr.io/hanmahong5-arch/lurus-newhub:staging
kubectl apply -k deploy/k8s/staging/ && kubectl -n lurus-staging rollout restart deployment/lurus-newhub
```

## Verify / Monitor

```bash
curl https://staging-api.lurus.cn/api/status                                    # liveness (DB-free)
curl https://staging-api.lurus.cn/api/health                                    # deep health (200/503)
# OAuth: visit https://staging-api.lurus.cn/api/v2/staging/auth/login → complete Zitadel
kubectl -n lurus-staging logs -f deployment/lurus-newhub                         # logs
# metrics: https://staging-api.lurus.cn/metrics (scraped by Netdata go.d prometheus
# collector — the OTLP→jaeger trace path is RETIRED, monitoring is Netdata now;
# see lurus/CLAUDE.md observability HARD RULE).
```

## Troubleshooting

```bash
kubectl -n lurus-staging describe pod -l app=lurus-newhub
kubectl -n lurus-staging get events --sort-by=.lastTimestamp
kubectl -n lurus-staging get certificate; kubectl -n cert-manager logs -l app=cert-manager      # certs
```

## Differences from Production

| Aspect | Production | Staging |
|--------|------------|---------|
| Replicas | 2 | 1 |
| Resources | 256Mi-1Gi / 100m-500m | 128Mi-512Mi / 50m-250m |
| Redis DB | 0 | 1 |
| Database | newhub | newhub (no isolated staging DB confirmed) |
| PDB | Yes (minAvailable:1) | No |

Cleanup: `kubectl delete namespace lurus-staging` (deletes resources but preserves the DB).
