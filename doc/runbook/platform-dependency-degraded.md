# Runbook — lurus-platform (platform-core) unreachable

> **Source**: operator observation, or `newhub_platform_breaker_open` once that
> alarm is installed (see `doc/runbook/platform-billing-breaker-open.md` for the
> breaker itself — this page is about everything *else* that touches
> platform-core).
> **Triggered by**: platform-core down, rolling, network-partitioned, or
> answering slowly. Symptoms reported from the outside are "the console spins",
> "relay got slower", "logins lose their account link".
> **Severity**: degraded, not outage — newhub keeps relaying.
> **Last review**: 2026-09-19 (cycle 12 L7).

## What talks to platform-core

Two transports to the same service, and every call tries them in this order:

1. gRPC — `IDENTITY_GRPC_ADDR`, default
   `platform-core.lurus-platform.svc.cluster.local:18105`.
2. HTTP twin — `IDENTITY_SERVICE_URL`, default
   `http://platform-core.lurus-platform.svc.cluster.local:18104`.

The wrappers are in `internal/pkg/common/identity_grpc_client.go`; the HTTP
twins in `internal/pkg/common/identity_client.go`. Callers are account lookup
on login (`internal/adapter/handler/oauth.go`,
`internal/adapter/middleware/admin_jwt_auth.go`,
`internal/adapter/middleware/oidc_auth.go`,
`internal/adapter/middleware/release_gate.go`) and, when
`BILLING_UNIFIED_ENABLED` is on, the wallet pre-auth/settle/debit legs from
`internal/app/quota.go`.

## Time bounds (cycle 12)

One logical identity call is bounded end to end, both transports together:

| Knob | Default | What it bounds |
|---|---|---|
| `IDENTITY_TIMEOUT_MS` | 5000 | the whole call — gRPC leg **plus** HTTP fallback |
| `IDENTITY_GRPC_TIMEOUT_MS` | 2000 | the gRPC leg alone, inside the total |

Before cycle 12 each leg had its own 5s, so one call could cost 10s, and the
gRPC client was built with wait-for-ready, which turned "connection refused"
into "block until the deadline" instead of failing over immediately. Both are
fixed; `internal/pkg/common/identity_timeout_test.go` is the oracle.

Consequence when platform-core is **down** (connection refused): the gRPC leg
returns in milliseconds, the HTTP leg burns what is left of the 5s, so a
console request that needs an account lookup is slower by up to ~5s, once, not
by 10s per lookup.

Consequence when platform-core is **hung** (accepts, never answers): the gRPC
leg costs 2s, the HTTP leg the remaining 3s. Same 5s ceiling.

## What each caller does with a failed lookup

- **Console / login**: the account link is skipped. The user still gets a
  session from the local user record; what is missing is the platform account
  binding, so wallet balance and topup links read as absent. It repairs itself
  on the next successful login once platform-core is back — no manual step.
- **Release download gate** (`internal/adapter/middleware/release_gate.go:72`):
  fails closed, and only for products listed in the gated-products config —
  free/public downloads are untouched. A failed account resolve aborts 401
  `DOWNLOAD_AUTH_REQUIRED`; an entitlement call that errors aborts 403
  `ENTITLEMENT_CHECK_FAILED`. Both are intentional; do not work around them.
- **Relay with unified billing off** (`BILLING_UNIFIED_ENABLED: "false"`,
  `deploy/k8s/r6-uat/deployment.yaml:144`): unaffected. The money legs are not
  called at all. Note the env var is not the whole truth — platform's
  `admin.settings.billing.unified_enabled` is the other input to the same
  decision, so check both before concluding the money path is idle.
- **Relay with unified billing on** (`"true"` on
  `deploy/k8s/r6-stage/deployment.yaml:170`): pre-auth fails, the circuit
  breaker opens after 3 consecutive failures, and the degrade path
  (`TryDegradedPreAuth`, `internal/pkg/common/billing_cache.go:146`) decides per
  request whether to admit on a fresh cached balance or fail closed with 402. See
  `doc/runbook/platform-billing-breaker-open.md`.

The breaker is opened and closed by the money legs only. Account and
entitlement lookups do not write breaker state: making them write it would let
an identity-side blip fast-fail the money path, and — on a deployment where
`BILLING_UNIFIED_ENABLED` is off, so no money leg ever probes — would leave the
breaker latched open with nothing to close it.

## Triage

```bash
# 1. Is platform-core actually up?
kubectl -n lurus-platform get pods
kubectl -n lurus-platform logs deploy/platform-core --tail=50

# 2. What does newhub think?
curl -fsS https://hub.lurus.cn/api/health | jq '.checks'
#    checks.billing: "ok" | "circuit_open" | "legacy_mode"
#    "legacy_mode" means BILLING_UNIFIED_ENABLED is off — not a fault.

# 3. Breaker state series (0 closed, 1 open, 2 half-open — see
#    internal/pkg/common/billing_breaker.go for which value each state sets).
curl -fsS http://localhost:30850/metrics | grep billing_circuit_breaker_state
```

## Levers

- Shrink the blast radius of a slow platform without a code change:
  `IDENTITY_TIMEOUT_MS=2000` on the deployment. Below ~1500 the HTTP fallback
  stops having a useful share of the budget.
- There is no switch that makes newhub stop calling platform-core for account
  lookups. The closest lever is `BILLING_UNIFIED_ENABLED=false`, which removes
  the money legs only.

## Other outbound dependencies with the same failure shape

Bounded in cycle 12, listed here so a future "everything is slow" triage has one
page to check:

| Dependency | Bound | Source |
|---|---|---|
| Redis | `REDIS_OP_TIMEOUT_MS` 1000, `REDIS_DIAL_TIMEOUT_MS` 2000, caller deadlines honoured | `internal/pkg/common/redis.go` |
| NATS JetStream publish | caller context, 5s for fire-and-forget events; unlimited reconnect after boot | `internal/pkg/nats/publisher.go` |
| Customer webhook notify | 10s | `internal/app/webhook.go` |
| Ali async image task poll | 30s per poll | `internal/adapter/provider/ali/image.go` |
| Ollama list/delete model | 30s | `internal/adapter/provider/ollama/relay-ollama.go` |

Known gap: the worker-relay branch of webhook delivery
(`internal/app/download.go:24` `DoWorkerRequest`) posts through
`GetHttpClient().Post`, which takes no context, so it is not bounded by the 10s
above. It is reached only when a worker URL is configured — that is a DB option
(`system_setting.WorkerUrl`), not an env var, so confirm the live value from the
admin settings page rather than from a manifest.

## NATS

`nats_connected` (gauge, `lurus_gateway_nats_connected`) is 1 when the
JetStream publisher holds a connection. It is **0 both when NATS is disabled and
when the broker is unreachable** — the publisher is never constructed in the
disabled case, so nothing writes it. Read it together with
`LLM_QUOTA_NATS_ENABLED` on the deployment before treating 0 as a fault: prod
sets it `"true"` (`deploy/k8s/r6-stage/deployment.yaml:217`), UAT `"false"`
(`deploy/k8s/r6-uat/deployment.yaml:148`), so 0 on UAT is expected and relay is
unaffected there.

One boot-time caveat: if the broker is unreachable when the pod starts, `Init`
logs the failure and the process runs with no publisher for its lifetime —
reconnect is unlimited only for a connection that was established at least
once. The log line to look for is `Failed to initialize NATS quota publisher`
(`cmd/server/main.go:599`); it is non-fatal by design. Remedy: restart the
deployment once the broker is back.
