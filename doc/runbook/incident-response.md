# Incident Response Runbook

> Service `lurus-newhub` · Namespace `lurus-newhub` · On R6 (Tailscale
> `100.122.83.20`; SSH fallback `-p 12222 root@43.226.45.87`). Public host
> `hub.lurus.cn`. On-call: Anita.

## Health checks

```bash
curl -s -o /dev/null -w "%{http_code}" https://hub.lurus.cn/api/status          # liveness, DB-free, expect 200
curl -s https://hub.lurus.cn/api/health | jq .                                  # deep: database/redis/billing/schema_migrations
ssh root@100.122.83.20 "kubectl exec -n lurus-newhub deploy/lurus-newhub -- wget -qO- http://localhost:3000/api/status"
ssh root@100.122.83.20 "kubectl get pods -n lurus-newhub -l app=lurus-newhub -o wide"   # expect Running 3/3
```
Probes: startup (`/api/status`, 5s period) and liveness (`/api/status`, 15s
period) are shallow — no dependency checks, so a DB/Redis blip never
restarts the process. Readiness (`/api/health`, 5s period) is deep — DB ping
+ breaker state — and gates traffic, not restarts (see
`deploy/k8s/r6-stage/deployment.yaml` ≈:264-266).

## Triage decision tree

```
Service unreachable?
├── Pod issue → CrashLoopBackOff (logs) / OOMKilled (↑mem) / ImagePullBackOff (see below) / Pending (node resources)
├── 5xx → app logs → DB conn error (database runbook) / Redis conn / panic (SafeGo, restart) / upstream LLM timeout (channel status)
└── Slow → resources → high CPU (pprof) / high mem (leak, ↑limit) / high DB latency (pg_stat_activity)
```

## Pod issues

```bash
# CrashLoopBackOff (causes: DB unreachable at startup / missing env / failed migration)
ssh root@100.122.83.20 "kubectl describe pod -n lurus-newhub -l app=lurus-newhub"
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deploy/lurus-newhub --previous"
# OOMKilled — limit is 1Gi (deploy/k8s/r6-stage/deployment.yaml); raising it is
# a manifest edit + merge to main (ArgoCD selfHeal reverts a live kubectl edit).
ssh root@100.122.83.20 "kubectl top pod -n lurus-newhub -l app=lurus-newhub"
# ImagePullBackOff — this manifest has no imagePullSecrets (public GHCR image,
# node-level containerd pull); if pulls are failing, suspect the node's GHCR
# credential or the digest in deployment.yaml not existing yet (Publish
# workflow still running).
ssh root@100.122.83.20 "kubectl describe pod -n lurus-newhub -l app=lurus-newhub | grep -A5 'Events'"
```

## Logs

```bash
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deploy/lurus-newhub --tail=200"
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deploy/lurus-newhub | grep -i 'error\|panic\|fatal'"
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deploy/lurus-newhub | grep 'relay'"   # or 'database'
```

| Log pattern | Meaning | Action |
|-------------|---------|--------|
| `failed to initialize database` | DB unreachable at startup | check DB host/creds/network (database.md) |
| `JWKS fetch failed` | can't reach the OIDC provider's JWKS endpoint | check `identity.lurus.cn`/network |
| `channel error` | upstream LLM failed | check channel config / provider |
| `quota exceeded` | over quota | check billing, adjust quota |
| `panic recovered by SafeGo` | goroutine panic caught | check stack trace, fix root cause |

## Resource monitoring

```bash
ssh root@100.122.83.20 "kubectl top pod -n lurus-newhub; kubectl top node"
# pprof (DEBUG=true / ENABLE_PPROF), from inside the cluster or via port-forward:
curl -o cpu.prof http://localhost:3000/debug/pprof/profile?seconds=30 && go tool pprof cpu.prof
curl -o mem.prof http://localhost:3000/debug/pprof/heap && go tool pprof mem.prof
curl http://localhost:3000/debug/pprof/goroutine?debug=2
```

```sql
SELECT count(*) FROM pg_stat_activity WHERE datname='newhub';
SELECT pid, now()-query_start AS duration, query FROM pg_stat_activity WHERE state='active' AND datname='newhub' ORDER BY duration DESC;
SELECT pg_terminate_backend(<pid>);
```

## Common scenarios

- **All relay failing**: check channel status (admin) → verify upstream key → `grep "channel error"` → test direct upstream from pod (`kubectl exec ... wget --header='Authorization: Bearer <key>' https://api.openai.com/v1/models`) → enable backup channels / notify.
- **High latency**: `kubectl top pod` → `pg_stat_statements` → `redis-cli -n 2 ping` (DB **2**, not 0) → pprof if sustained. (Meilisearch is not deployed for this service — `MEILISEARCH_ENABLED=false`.)
- **Tenant login broken**: `curl https://identity.lurus.cn/oauth/v2/keys` → verify OIDC config (client ID, redirect URI, issuer) → check JWT validation errors → verify tenant in `tenants` table → test callback with `curl -v`.

## Escalation

| Sev | Criteria | Response |
|-----|----------|----------|
| P0 | Service down, all users | Immediate; rollback if recent deploy |
| P1 | Major feature (relay/auth), >50% users | Within 1h; fix or rollback |
| P2 | Single tenant / degraded | Within 4h |
| P3 | Minor, workaround exists | Next business day |

On-call: Anita (all incidents); Infrastructure (DB host, K3s node) for P0/P1 infra.

## Recovery commands

⚠️ The ArgoCD Application tracking this deployment runs `automated + selfHeal`
— a live `kubectl rollout restart` / `kubectl set image` / `kubectl scale`
edit gets reverted on the next sync, so it only buys a few seconds and
manufactures an "I fixed it but it un-fixed itself" false signal. The real
rollback path is **git revert the auto-pin commit** (see
`doc/runbook/staging-deploy.md` § Rollback) or, if ArgoCD itself is down,
`scripts/stage-rollback.sh` / `SKIP_SECRETS=1 bash scripts/deploy-stage.sh`.
The commands below are for genuine emergencies (e.g. ArgoCD unreachable and a
pod is actively taking the service down) where a temporary manual action
buys time until the git-level fix lands.

```bash
ssh root@100.122.83.20 "kubectl delete pod <pod> -n lurus-newhub --force --grace-period=0"   # non-persistent, safe under selfHeal
ssh root@100.122.83.20 "kubectl get all -n lurus-newhub"
# `kubectl scale --replicas=0` is a spec change ArgoCD selfHeal reverts too —
# only use it after pausing/deleting the Application (argocd app get
# lurus-newhub / argocd app delete --cascade=false). The supported
# rollback path is the pin revert in doc/runbook/staging-deploy.md
# (section "Rollback"); ArgoCD CLI access itself is described there.
```

## Postmortem (after P0/P1)

Create `doc/audit/YYYY-MM-DD-title.md` (the repo's actual postmortem/incident-writeup location — see e.g. `doc/audit/2026-06-01-ci-red-diagnosis-and-followups.md`) with: Date, Duration, Severity, Impact, Timeline, Root Cause, Resolution, Action Items.
