# Tenant Onboarding Runbook

> Auth: OIDC (vendor-neutral; issuer is deploy-time owner-gated) · API: `hub.lurus.cn` ·
> Namespace `lurus-newhub` · R6 Tailscale `100.122.83.20` (SSH fallback `-p 12222 root@43.226.45.87`) ·
> Flow: OIDC Org → API Tenant Record → **credit pool** → User Identity Mapping.

Two modes: **auto-create** (`OIDC_AUTO_CREATE_TENANT=true` — tenant created on first
user login) or **manual** (admin creates tenant via API, maps to the OIDC Org ID).

⚠️ **A tenant with no credit pool row relays for free (or 402s) depending on
`CREDIT_POOL_REQUIRED`** (`log` = bypass-but-counted, `enforce` = HTTP 402
`pool_not_configured`, `off` = no gate at all — `internal/pkg/setting/credit_pool_setting.go`).
Under `enforce`, every new tenant MUST get a credit-pool row (Phase 2b below)
before its users can relay, or their very first request 402s.

## Phase 1: OIDC Provider Setup (manual)

Create an Organization (record the Org ID), an OIDC Application (Web/PKCE,
redirect `https://hub.lurus.cn/api/v2/oauth/callback`, post-logout
`https://hub.lurus.cn/`, Grant Types Authorization Code + Refresh Token,
RS256 JWT), and roles `admin` / `user` / `billing_manager`. **Full step-by-step:
`doc/oidc-setup-guide.md`.**

`OIDC_CLIENT_ID` is written into the `lurus-newhub-secrets` Secret out-of-band
by the platform app-registry reconciler after IdP registration (5-min poll) —
do NOT hand-write it; a manual value is overwritten on the next tick. See
`deploy/k8s/r6-stage/secret-template.yaml` for the full secret schema and
`scripts/deploy-stage.sh` for the rotation path for the other keys.

## Phase 2: API Tenant Creation

**Option A — auto-create (recommended for OIDC-first tenants)**: with
`OIDC_AUTO_CREATE_TENANT=true`, the first login from a new OIDC Org
auto-creates the tenant. No API call — but Phase 2b (credit pool) still
applies once the tenant exists, or its first relay 402s.

**Option B — manual via admin API**:

```bash
curl -X POST https://hub.lurus.cn/api/v2/admin/tenants \
  -H "Content-Type: application/json" -H "Cookie: session=<root_admin_session>" \
  -d '{"zitadel_org_id":"<oidc_org_id>","slug":"acme-corp","name":"Acme Corporation","plan_type":"pro","max_users":500,"max_quota":5000000}'
# 201 → {"success":true,"data":{"id":"uuid","zitadel_org_id":...,"slug":...,"status":1}}
```

> NOTE(idp-migration): the admin-API JSON key `zitadel_org_id` is kept for
> back-compat (it equals the physical column name `tenants.zitadel_org_id`). The
> Go field/concept is neutralized to IDPOrgID; the wire key flips to `idp_org_id`
> only alongside the DB column rename (owner-gated migration).

Tenant status: `1` Enabled · `2` Disabled (login blocked, data preserved) · `3` Suspended (login + API blocked).

## Phase 2b: Credit Pool (real 3-step — required under `CREDIT_POOL_REQUIRED=enforce`)

1. **Create the tenant** (Phase 2 above) — need its `id` for the next step.
2. **Create the pool row**, RootJWTAuth (root admin token, not a tenant session):

   ```bash
   curl -X POST https://hub.lurus.cn/api/v2/admin/tenants/<id>/credit-pool \
     -H "Content-Type: application/json" -H "Cookie: session=<root_admin_session>" \
     -d '{"max_balance": 1000000, "reset_period": "monthly", "alert_threshold_pct": 80}'
   ```

   `CreateCreditPool` only accepts `max_balance` / `reset_period` /
   `alert_threshold_pct` — `current_balance` is not a request field at all,
   the pool row is always created with `current_balance = 0` server-side
   (`repo.CreateTenantCreditPool`). That starting-at-0 with `max_balance > 0`
   is intentional — it proves the relay 402s (`pool_exhausted`) BEFORE step 3
   funds it, rather than silently bypassing because no row existed at all
   (`pool_not_configured` would bypass to 200 under `CREDIT_POOL_REQUIRED=log`,
   which hides the gap instead of surfacing it).

3. **Fund the pool from platform** (billing side, not this API) — the paid
   subscription flow POSTs the fund event to newhub's internal seam:

   ```bash
   # Called BY platform-core, X-API-Key scoped balance:write — shown here for
   # manual/drill use only, e.g. to validate the seam in isolation:
   curl -X POST https://hub.lurus.cn/internal/v1/provisioning/tenants/<slug>/credit-pool/fund \
     -H "X-API-Key: $NEWHUB_INTERNAL_API_KEY" -H "Content-Type: application/json" \
     -d '{"event_id":"manual-fund-1","amount":12000,"source":"manual","account_id":<platform_account_id>}'
   ```

   Idempotent on `(tenant_id, event_id)` — replaying the same `event_id` for
   the same tenant is a no-op 200, not a double-fund.

**The 402 you'll see if you skip this**: `pool_not_configured` (no row,
`CREDIT_POOL_REQUIRED=enforce`) or `pool_exhausted` (row exists,
`current_balance` has hit 0) — both come back from `PoolBalanceCheck`
(`internal/adapter/middleware/pool_balance_check.go`) before the relay call
ever reaches an upstream provider, so an empty pool costs nothing to detect.

## Phase 3: User First Login (automatic)

`/api/v2/acme-corp/auth/login` → 302 to the provider authorize endpoint → provider
login/consent → 302 to `/api/v2/oauth/callback?code&state` → exchange code →
OIDCAuth middleware auto-maps the user → session created.

Automatic steps: JWT validated via JWKS → tenant resolved from the configurable
org-id claim (`OIDC_CLAIM_ORG_ID`, default `org_id`) → `tenants.zitadel_org_id`
(physical column) → user mapped from the `sub` claim → `user_identity_mapping`
row → Lurus user created with the tenant-plan default quota → tenant context
injected for isolation.

## Phase 4: Verification

```bash
curl -s https://hub.lurus.cn/api/v2/admin/tenants -H "Cookie: session=<admin_session>" | jq '.data[] | {id,slug,name,status}'
ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \"SELECT id, slug, name, status, plan_type FROM tenants;\""
ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \"SELECT tenant_id, current_balance, max_balance FROM tenant_credit_pools WHERE tenant_id='<id>';\""
# Physical columns retain the zitadel_ prefix until the rename migration lands:
ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \"SELECT zitadel_user_id, lurus_user_id, tenant_id, email FROM user_identity_mapping WHERE tenant_id='<tenant_id>';\""
curl -v "https://hub.lurus.cn/api/v2/acme-corp/auth/login?redirect_url=/dashboard"   # expect 302 → provider authorize endpoint
```

## Phase 5: Tenant Management

| Operation | Endpoint | Method |
|-----------|----------|--------|
| List / Create | `/api/v2/admin/tenants` | GET / POST |
| Get / Update | `/api/v2/admin/tenants/:id` | GET / PUT |
| Enable / Disable / Suspend | `/api/v2/admin/tenants/:id/{enable,disable,suspend}` | POST |
| Stats | `/api/v2/admin/tenants/:id/stats` | GET |
| Credit pool: create/get/topup/usage/delete | `/api/v2/admin/tenants/:id/credit-pool[/topup\|/usage]` | POST/GET/POST/GET/DELETE |

```bash
curl -X POST https://hub.lurus.cn/api/v2/admin/tenants/<id>/disable -H "Cookie: session=<admin_session>"  # all users lose login, data preserved
curl -X PUT https://hub.lurus.cn/api/v2/admin/tenants/<id> -H "Content-Type: application/json" -H "Cookie: session=<admin_session>" -d '{"max_quota":10000000,"max_users":1000}'
```

## Troubleshooting

| Problem | Check |
|---------|-------|
| Login redirects but never completes | OIDC redirect URI matches exactly |
| "Tenant not found" on login | `OIDC_AUTO_CREATE_TENANT=true` or create manually |
| JWT verification fails | `OIDC_ISSUER`, JWKS endpoint reachable |
| User not created on login | `OIDC_AUTO_CREATE_USER=true` |
| Roles empty / RBAC denies | `OIDC_CLAIM_ROLES` matches the provider's roles claim key |
| Cross-tenant data visible | tenant_id in request context, GORM plugin |
| First relay call 402s `pool_not_configured` | no credit-pool row yet — run Phase 2b step 2 |
| First relay call 402s `pool_exhausted` | pool row exists but `current_balance=0` — run Phase 2b step 3 (fund) |

Env: `OIDC_ENABLED=true`, `OIDC_ISSUER=<deploy-time>`, `OIDC_CLIENT_ID`
(reconciler-owned, see Phase 1), `OIDC_REDIRECT_URI=https://hub.lurus.cn/api/v2/oauth/callback`,
`OIDC_JWKS_URI=<discovery jwks_uri>`, `OIDC_AUTO_CREATE_TENANT=true`,
`OIDC_AUTO_CREATE_USER=true`, `OIDC_ENABLE_PKCE=true`, `CREDIT_POOL_REQUIRED=enforce|log|off`.
Full set + configurable claim keys: `.env.oidc.example` / `doc/oidc-setup-guide.md`.
