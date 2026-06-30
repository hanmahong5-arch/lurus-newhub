# Tenant Onboarding Runbook

> Auth: OIDC (vendor-neutral; issuer is deploy-time owner-gated) · API: api.lurus.cn ·
> Flow: OIDC Org → API Tenant Record → User Identity Mapping.

Two modes: **auto-create** (`OIDC_AUTO_CREATE_TENANT=true` — tenant created on first
user login) or **manual** (admin creates tenant via API, maps to the OIDC Org ID).

## Phase 1: OIDC Provider Setup (manual)

Create an Organization (record the Org ID), an OIDC Application (Web/PKCE,
redirect `https://api.lurus.cn/api/v2/oauth/callback`, post-logout
`https://api.lurus.cn/logout`, Grant Types Authorization Code + Refresh Token,
RS256 JWT), and roles `admin` / `user` / `billing_manager`. **Full step-by-step:
`doc/oidc-setup-guide.md`.**

Update the K8s secret + restart:

```bash
kubectl create secret generic lurus-api-secrets \
  --from-literal=OIDC_CLIENT_ID='<client_id>' --from-literal=OIDC_CLIENT_SECRET='<client_secret>' \
  --from-literal=SQL_DSN='postgres://...' --from-literal=SESSION_SECRET='...' \
  -n lurus-system --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart deployment/lurus-api -n lurus-system
```

## Phase 2: API Tenant Creation

**Option A — auto-create (recommended)**: with `OIDC_AUTO_CREATE_TENANT=true`, the
first login from a new OIDC Org auto-creates the tenant. No API call.

**Option B — manual via admin API**:

```bash
curl -X POST https://api.lurus.cn/api/v2/admin/tenants \
  -H "Content-Type: application/json" -H "Cookie: session=<platform_admin_session>" \
  -d '{"zitadel_org_id":"<oidc_org_id>","slug":"acme-corp","name":"Acme Corporation","plan_type":"pro","max_users":500,"max_quota":5000000}'
# 201 → {"success":true,"data":{"id":"uuid","zitadel_org_id":...,"slug":...,"status":1}}
```

> NOTE(idp-migration): the admin-API JSON key `zitadel_org_id` is kept for
> back-compat (it equals the physical column name `tenants.zitadel_org_id`). The
> Go field/concept is neutralized to IDPOrgID; the wire key flips to `idp_org_id`
> only alongside the DB column rename (owner-gated migration).

Tenant status: `1` Enabled · `2` Disabled (login blocked, data preserved) · `3` Suspended (login + API blocked).

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
curl -s https://api.lurus.cn/api/v2/admin/tenants -H "Cookie: session=<admin_session>" | jq '.data[] | {id,slug,name,status}'
psql "$DSN" -c "SELECT id, slug, name, status, plan_type FROM tenants;"
# Physical columns retain the zitadel_ prefix until the rename migration lands:
psql "$DSN" -c "SELECT zitadel_user_id, lurus_user_id, tenant_id, email FROM user_identity_mapping WHERE tenant_id='<tenant_id>';"
curl -v "https://api.lurus.cn/api/v2/acme-corp/auth/login?redirect_url=/dashboard"   # expect 302 → provider authorize endpoint
```

## Phase 5: Tenant Management

| Operation | Endpoint | Method |
|-----------|----------|--------|
| List / Create | `/api/v2/admin/tenants` | GET / POST |
| Get / Update | `/api/v2/admin/tenants/:id` | GET / PUT |
| Enable / Disable / Suspend | `/api/v2/admin/tenants/:id/{enable,disable,suspend}` | POST |
| Stats | `/api/v2/admin/tenants/:id/stats` | GET |

```bash
curl -X POST https://api.lurus.cn/api/v2/admin/tenants/<id>/disable -H "Cookie: session=<admin_session>"  # all users lose login, data preserved
curl -X PUT https://api.lurus.cn/api/v2/admin/tenants/<id> -H "Content-Type: application/json" -H "Cookie: session=<admin_session>" -d '{"max_quota":10000000,"max_users":1000}'
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

Env: `OIDC_ENABLED=true`, `OIDC_ISSUER=<deploy-time>`, `OIDC_CLIENT_ID`/`_SECRET`
(Phase 1), `OIDC_REDIRECT_URI=https://api.lurus.cn/api/v2/oauth/callback`,
`OIDC_JWKS_URI=<discovery jwks_uri>`, `OIDC_AUTO_CREATE_TENANT=true`,
`OIDC_AUTO_CREATE_USER=true`, `OIDC_ENABLE_PKCE=true`. Full set + configurable
claim keys: `.env.oidc.example` / `doc/oidc-setup-guide.md`.
