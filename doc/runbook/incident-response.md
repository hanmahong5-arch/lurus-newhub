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
- **Locked out of step-up verification (`SECURE_VERIFICATION_REQUIRE_ENROLLMENT=true`)**: this flag makes
  `POST /api/verify` (`method:"session"`) refuse with `403 STEP_UP_ENROLLMENT_REQUIRED` for any admin with
  no TOTP enrollment — which blocks channel-key reveal and 2FA force-disable for that account. If an
  operator gets locked out (flag was turned on before that operator enrolled a factor), break-glass is:
  set `SECURE_VERIFICATION_REQUIRE_ENROLLMENT=false` (or unset it) in the deployment's secret/env and
  restart the pods — this is a plain env change through the normal deploy path (`deploy/k8s/r6-stage/`),
  not a runtime toggle, so it goes through the same ArgoCD/manifest flow as any other config change. The
  no-enrollment grant this restores is handed to the audit writer on every pass through that branch
  (`auth.stepup_without_credential` in the audit trail, a best-effort background insert — dropped if no
  writer is registered, logged but not retried on failure) regardless of the flag, so turning it off does
  not reopen an invisible hole — it reopens an auditable one, same as before this flag existed. (While
  the flag is on, a refused attempt is also audited, as `auth.failed` with `reason:enrollment_required`.)
  Note (cycle-12 L4): "locked out" above means *refused until that account enrols*, not stranded —
  enrolment itself is not behind the step-up gate (`POST /api/user/totp/enroll` and `/confirm` carry
  `UserAuth` + `CriticalRateLimit` only, `router/api-router.go:78-79`), so an operator who can still log
  in can self-serve. Break-glass is for an operator who enrolled and then lost the factor.
- **"Session registry is disabled on this deployment" (HTTP 409 `SESSION_REGISTRY_DISABLED`)**: the
  per-device session endpoints refuse rather than pretend. Affected:
  `DELETE /api/v2/admin/users/:id/sessions` (the compromised-account runbook step) and
  `DELETE /api/v2/:tenant_slug/sessions/others` ("sign out other devices"). This is a configuration
  state, not a fault — it means `SESSION_REGISTRY_ENABLED` is not `"true"` on the replica that answered.

  Where the switch lives: it is a plain env var, read per call by
  `repo.SessionRegistryEnabled()` (`internal/adapter/repo/user_session.go:48`, exact string `"true"`).
  It is set in `deploy/k8s/r6-uat/deployment.yaml` (UAT: `"true"`, on since the cycle-7 soak) and is
  **absent from `deploy/k8s/r6-stage/deployment.yaml`**, i.e. off in production as of 2026-09-19.
  Turning it on in production is a manifest edit merged to `main` (ArgoCD reverts a live `kubectl set
  env`), which is owner item O-session — not an in-incident action.

  Until it is on: to terminate a compromised session in production, the working levers are the user's
  own logout (`DELETE /api/v2/:tenant_slug/sessions/current`, which clears the cookie and deletes the
  Redis session key regardless of this flag), disabling the user
  (`PUT /api/user/` with `status`, which `authHelper` re-checks against the user cache on every
  request), or deleting the store's session key directly in Redis
  (`redis-cli -n 2 DEL session_<key>`; DB **2**, not 0).

  Before cycle-12 L4 these two endpoints answered `200 {"revoked":0}` with the flag off — success
  shaped, nothing done. If a past incident record says sessions were revoked on production, check
  whether it was one of these calls.

  The READ side answers the same configuration state rather than inventing one:
  `GET /api/v2/:tenant_slug/sessions` returns `{"items":[],"total":0,"registry_enabled":false}` with
  the flag off (cycle-12 L4; it used to return one synthetic row — id `current`, `last_seen` = now —
  for every caller, so a user signed in from five browsers was shown exactly one device). The console's
  Security panel reads `registry_enabled` and says the feature is not enabled on this deployment.
  **Do not read an empty session list on production as "this account has no live sessions"** — with the
  flag off the list says nothing at all about how many devices hold a valid cookie. The `users` +
  Redis `session_*` keys are the only evidence in that state.
- **"cross-site request refused" (HTTP 403 `CROSS_SITE_REQUEST`)**: `middleware.BrowserOriginGuard`
  refused a cookie-authenticated, state-changing request that the browser itself reported as coming
  from another site (`Sec-Fetch-Site: same-site|cross-site`), or whose `Origin` is absent from
  `ALLOWED_ORIGINS`. Requests carrying `Authorization` or `X-API-Key`, and requests with no session
  cookie, are never refused by it — so relay clients, service callers and `POST /api/v2/bridge/exchange`
  cannot produce this.

  If a legitimate integration is being refused, the lever is `CSRF_ORIGIN_GUARD_MODE`:
  `enforce` (default, and what any unrecognised value means), `observe` (admit, count under
  `lurus_gateway_csrf_observed_total{reason}`, log one line per reason per minute) or `off` (the guard
  evaluates nothing). Read per request, so it takes effect on the next request — but it is still an env
  var in the manifest, so changing it on production is a merged manifest edit, not a live `kubectl set
  env` (ArgoCD selfHeal reverts that). `observe` is the diagnosis setting; the correct fix for a real
  integration is a credential header, because a CORS allowlist entry does not make another site safe to
  act with a victim's cookie. `lurus_gateway_csrf_rejected_total{reason}` counts only actual refusals —
  it stays at 0 in observe and off.

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

Create `doc/audit/YYYY-MM-DD-title.md` (the repo's postmortem/incident-writeup location) with: Date, Duration, Severity, Impact, Timeline, Root Cause, Resolution, Action Items.
