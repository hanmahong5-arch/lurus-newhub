# Deployment Runbook

> Service lurus-api · Namespace lurus-system · Host api.lurus.cn. All `kubectl`/`argocd` via `ssh root@100.98.57.55`. Quick 5-min deploy: `DEPLOY.md` (repo root).

## 1. Build

CI/CD (GitOps): push to `main` → `.github/workflows/docker-image-main.yml` → `ghcr.io/LurusTech/lurus-api:main-YYYYMMDD-<sha>` → ArgoCD auto-sync.

```bash
# Manual local build
cd web && bun install && bun run build
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o lurus-api ./cmd/server
docker build -t lurus-api:local .
```

Image tags: `main` → `main-YYYYMMDD-<sha>` (GHCR); `alpha` → `alpha-YYYYMMDD-<sha>` (GHCR + Docker Hub); release tag → `v1.2.3`/`latest` (GHCR + Docker Hub).

## 2. Deploy

Prereqs: K3s access, `kubectl` for lurus-system, ArgoCD synced.

```bash
# ArgoCD (standard): push to main → CI builds → auto-sync
git push origin main
kubectl get application lurus-api -n argocd -o jsonpath='{.status.sync.status}'
# Manual override
kubectl set image deployment/lurus-api lurus-api=ghcr.io/LurusTech/lurus-api:<tag> -n lurus-system
kubectl rollout restart deployment/lurus-api -n lurus-system
kubectl apply -k deploy/k8s/                      # full manifest update
```

## 3. Verify

```bash
curl -s -o /dev/null -w "%{http_code}" https://api.lurus.cn/api/status                          # 200
kubectl exec -n lurus-system deploy/lurus-api -- wget -qO- http://localhost:3000/api/status
kubectl get pods -n lurus-system -l app=lurus-api; kubectl describe pod -n lurus-system -l app=lurus-api
kubectl logs -n lurus-system deploy/lurus-api --tail=100        # add -f to follow, --previous after crash
curl -s https://api.lurus.cn/api/status | jq .
curl -s -H "Authorization: Bearer <token>" https://api.lurus.cn/api/v2/<tenant>/tokens | jq .   # v2 smoke
```

## 4. Rollback

```bash
# ArgoCD
kubectl get application lurus-api -n argocd -o jsonpath='{.status.history}' | jq
argocd app rollback lurus-api
# kubectl
kubectl rollout history deployment/lurus-api -n lurus-system
kubectl rollout undo deployment/lurus-api -n lurus-system [--to-revision=<N>]
kubectl rollout status deployment/lurus-api -n lurus-system
# Pin specific image
kubectl set image deployment/lurus-api lurus-api=ghcr.io/LurusTech/lurus-api:main-20260201-abc1234 -n lurus-system
```

## 5. Configuration

```bash
kubectl get secret lurus-api-secrets -n lurus-system -o yaml                       # view (base64)
kubectl create secret generic lurus-api-secrets --from-literal=SQL_DSN='postgres://...' --from-literal=SESSION_SECRET='...' \
  -n lurus-system --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart deployment/lurus-api -n lurus-system                       # pick up new secrets
```

| Variable | Source | Required |
|----------|--------|----------|
| `SQL_DSN` | Secret | Yes (PostgreSQL) |
| `SESSION_SECRET` | Secret | Yes |
| `REDIS_CONN_STRING` | ConfigMap | Optional (in-memory if unset) |
| `OIDC_CLIENT_ID` / `_SECRET` | Secret | For v2 auth |
| `MEILI_API_KEY` | Secret | For search |
| `NODE_TYPE` | Env | `master` (default) or `slave` |

## 6. Resource Limits

CPU req 100m / lim 500m; Memory req 256Mi / lim 1Gi. Adjust in `deploy/k8s/deployment.yaml` if OOMKilled or throttled.

## 7. Pre-Deploy Checklist

- [ ] `go test ./...` passes
- [ ] No secrets in code (check `.gitignore`)
- [ ] DB migration reviewed (GORM AutoMigrate on master startup)
- [ ] `/api/status` responds
- [ ] ArgoCD: Synced + Healthy
