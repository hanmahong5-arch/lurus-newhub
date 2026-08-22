# Runbook — SEAM S1 activation (platform entitlement → newhub credit-pool)

> Status 2026-06-20: **BLOCKED on newhub redeploy.** Both code sides are shipped
> and contract-matched (verified at HEAD). The chain cannot light up because
> **newhub is not running on R6** — it was alive on 2026-06-12 (ns `lurus-newhub`,
> per `lurus/doc/coord/contracts.md`) but has since been torn down. Live cluster
> now: `lurus-system` runs only the OSS `lurus-newapi` base; no `lurus-newhub`
> namespace; the `newhub` database is absent from the PG backup/whitelist set.

## What SEAM S1 is

On a paid subscription activation, platform `platform-core` POSTs the tenant's
credit-pool fund request to newhub so the LLM relay stops returning 402.

- **newhub side** (shipped): `POST /internal/v1/provisioning/tenants/:slug/credit-pool/fund`
  (`InternalFundCreditPool`, scope `balance:write`, idempotent on `event_id` via
  `credit_pool_fund_events` UNIQUE — migration 019). Relay gate `PoolBalanceCheck`
  returns 402 `pool_exhausted` on an exhausted pool, bypasses (200) on no-row /
  unlimited / healthy.
- **platform side** (shipped): `internal/module/creditpool.go` rides the
  plan-changed outbox/DLQ path; reads plan features `newhub_pool_slug` +
  `newhub_pool_amount`; POSTs the matching contract to `NEWHUB_BASE_URL`.

## Acceptance (goal)

Sandbox paid subscription activates → pool balance > 0 → relay flips 402 → 200.

---

## ⚠️ Named-artifact corrections (the goal text vs committed reality)

The goal as phrased carries four artifact mismatches. **Use the corrected
column** (per §4: do not silently substitute; these are the committed-manifest
truths, which is the source of truth chosen for this work).

| Goal text | Corrected (committed artifact) | Why |
|---|---|---|
| `NEWHUB_BASE_URL=http://lurus-newhub.lurus-newhub.svc:18200` | `http://lurus-newhub.lurus-staging.svc:8850` | `deploy/k8s/r6-stage/` deploys to ns `lurus-staging`, Service `lurus-newhub`, port `8850`→targetPort `3000`. ns `lurus-newhub` and port `18200` are both wrong. |
| `R1 platform-core-secrets` | R6, ns `lurus-platform` | `platform-core` runs on the STAGE cluster (`lurus-platform` ns, 2/2). R1 is intentionally empty. |
| tenant slug `lurus-default` | slug = **`lurus`** | Both seeds set `slug='lurus'`. `lurus-default` is the tenant **id** (and OIDC-org placeholder), not the slug. The fund endpoint resolves by `GetTenantBySlug` → plan feature `newhub_pool_slug` **must be `lurus`** or fund 404s → DLQ → false-fail. |
| `GET /api/v2/tenants/lurus-default/credit-pool` | `GET /api/v2/admin/tenants/default/credit-pool` (RootJWT) **or** `GET /api/v2/lurus/credit-pool/me` (OIDC) | Admin route is under `/admin`, keyed by tenant **id** (`default` on fresh 021 boot), RootJWTAuth. The slug-keyed end-user view is `/api/v2/:tenant_slug/credit-pool/me`. |

---

## Part A — Redeploy newhub to R6  (owner / needs `kubectl apply` + SSH; not doable via the read-mostly MCP)

Cluster access: `ssh root@100.122.83.20` (R6, Tailscale).

1. **Create the `newhub` database** on the PG pod (ns `database`, pod `lurus-pg-1`;
   the `newhub` db is absent today). Role `lurus` owns it:
   ```
   kubectl exec -n database lurus-pg-1 -- psql -U postgres -c \
     "CREATE DATABASE newhub OWNER lurus;"
   ```
   On first boot newhub auto-builds the schema (GORM auto-migrate + embedded
   migration runner; 021 baseline seeds tenant `id=default slug=lurus` + 16
   tenant_configs). No manual DDL needed.

2. **Create the secret** `lurus-newhub-secrets` in ns `lurus-staging` with REAL
   values (schema in `deploy/k8s/r6-stage/secret-template.yaml`). `SQL_DSN` must be
   `postgres://lurus:<pw>@lurus-pg-1.database.svc.cluster.local:5432/newhub?sslmode=disable`
   (non-`postgres://` DSN → boot fast-fail by design):
   ```
   kubectl -n lurus-staging create secret generic lurus-newhub-secrets \
     --from-literal=SESSION_SECRET='<...>' \
     --from-literal=SQL_DSN='postgres://lurus:<pw>@lurus-pg-1.database.svc.cluster.local:5432/newhub?sslmode=disable' \
     --from-literal=IDENTITY_SERVICE_INTERNAL_KEY='<platform internal key, scope balance:write>' \
     --from-literal=IDENTITY_SESSION_SECRET='<...>' \
     --from-literal=LURUS_WHITELABEL_MASTER_SECRET="$(openssl rand -hex 32)"
   ```
   `IDENTITY_SERVICE_INTERNAL_KEY` is the newhub→platform Bearer key ONLY.
   The fund endpoint runs the OTHER direction: platform calls it with
   `X-API-Key: <NEWHUB_API_KEY>` — a separate newhub-issued `lurus_ik_*` key
   (scope `balance:write`), and since 2026-08-22 that key must additionally be
   ScopeAll or have an `(api_key_id, tenant_id)` row in
   `internal_api_key_tenants` for each target tenant, else the fund call
   returns 403 `TENANT_NOT_AUTHORIZED`. (Corrected 2026-08-22 — this line
   previously claimed the two directions share one key; they never did.)

3. **Apply the overlay** (creates ns, deployment, NodePort service 8850:30850,
   NATS egress netpol; image `ghcr.io/hanmahong5-arch/lurus-newhub:main`):
   ```
   kubectl apply -k deploy/k8s/r6-stage/
   kubectl -n lurus-staging rollout status deploy/lurus-newhub
   ```
   Expect `1/1` and `GET /api/status` healthy. Confirm migrations applied
   (`schema_migrations` highest = 21).

---

## Part B — Configure the funding chain  (mixed; secret writes = owner, rollout/verify = drivable)

4. **Create the pool** for the seeded tenant (max > 0, balance starts 0 so the
   relay is provably 402 before funding). Tenant id = `default` on fresh boot:
   ```
   POST /api/v2/admin/tenants/default/credit-pool   # RootJWTAuth (admin token)
   { "max_balance": 1000000, "current_balance": 0 }
   ```
   (Without an existing pool row the relay BYPASSES to 200 — a pool with
   `current_balance=0, max_balance>0` is what produces the 402 to flip.)

5. **Add the newhub product plan** in platform via the admin API (not raw SQL).
   Routes (RootJWT admin): `POST /admin/products` (if no product yet) →
   `POST /admin/products/:id/plans` (`AdminCreatePlan`). The plan's `features`
   carry the pool grant — **slug is `lurus`** (not `lurus-default`):
   ```json
   "features": { "newhub_pool_slug": "lurus", "newhub_pool_amount": 12000 }
   ```

6. **Set `NEWHUB_BASE_URL`** for platform-core (ns `lurus-platform`) to
   `http://lurus-newhub.lurus-staging.svc:8850`, then roll out.
   ⚠️ Verified 2026-06-20 against the live cluster: this key is **absent** from
   `platform-core-secrets` (53 keys, no `NEWHUB_BASE_URL`) — so this is an ADD,
   not an edit. Set it where platform-core reads config (secret key or
   deployment env — `internal/pkg/config/config.go` reads `NEWHUB_BASE_URL`;
   empty value disables the module). Also confirm `NEWHUB_API_KEY` is populated —
   the fund call sends it as `X-API-Key` (newhub-issued `lurus_ik_*`, scope
   `balance:write`, and ScopeAll-or-whitelisted per the 2026-08-22 tenant guard;
   NOT the platform `INTERNAL_API_KEY`, which newhub's keyed store never accepts):
   ```
   # secret/env write = owner (kubectl); rollout is drivable via MCP rollout_restart
   kubectl -n lurus-platform rollout restart deploy/platform-core
   ```

---

## Part C — Acceptance smoke

The HTTP probes below run from inside the cluster / a Tailscale host (owner side).
What is drivable via the read-mostly MCP from here: `pg_query` on the pool balance
(once the `newhub` db is whitelisted), `rollout_restart`, `pod_logs`, `get_events`,
`rollout_status` — i.e. observe + restart, not issue relay HTTP.

1. **Before funding** — relay returns 402:
   ```
   curl -s -o /dev/null -w '%{http_code}' \
     -H "Authorization: Bearer <tenant-token>" \
     http://lurus-newhub.lurus-staging.svc:8850/v1/chat/completions -d '{...}'
   # expect 402  (pool_exhausted)
   ```
2. **Trigger** the sandbox paid subscription → platform outbox drains → fund POST.
   Platform already has `scripts/e2e-live-full-flow.sh` covering the
   checkout→subscription half (asserts `identity.product_plans`/`subscriptions`);
   pair it with the probes here for the newhub pool→relay half it does not cover.
   (Or fund directly to validate the seam in isolation:
   `POST /internal/v1/provisioning/tenants/lurus/credit-pool/fund`
   `{ "event_id":"smoke-1", "amount":12000, "source":"smoke", "account_id":1 }`
   — note slug `lurus` in the path.)
3. **After funding** — balance > 0 and relay returns 200:
   ```
   GET /api/v2/admin/tenants/default/credit-pool   # current_balance > 0
   curl ... /v1/chat/completions                    # expect 200
   ```

---

## Doc-debt to correct after activation (flagged, not yet edited — RL: surgical)

- `2l-svc-platform` `internal/module/creditpool.go` header comment claims
  "newhub side has been LIVE on R6 since 2026-06-12" — false since teardown;
  also the `CreditPoolConfig.NewhubBaseURL` example string uses the wrong
  `lurus-newhub.svc:18200`. Update to `lurus-staging.svc:8850` once confirmed.
- `lurus/doc/coord/contracts.md` SEAM S1 section: "newhub side LIVE on R6" and
  "slug `lurus-default`" are stale/swapped (slug is `lurus`; id is `lurus-default`).
- After redeploy, reconcile the ns/slug truth across service-status + contracts
  (a single owning correction, per the coord protocol).
