# OIDC Setup Guide / OIDC 配置指南

> Purpose: configure a vendor-neutral OIDC provider as the auth center for the
> lurus-api multi-tenant SaaS. newhub is a standard OIDC client/resource server —
> it validates RS256 JWTs against the provider JWKS and maps the provider's
> organization + roles claims onto tenants/roles. Any standard OIDC provider works
> (the platform target is Casdoor; examples below are illustrative).
>
> All instance values (issuer host, client id/secret, org id) are deploy-time and
> owner-gated — none are hardcoded in the codebase.

newhub needs, from the provider: one Organization (= tenant in lurus-api), one
OIDC Application (client), Roles, and a JWKS endpoint. Each org has independent
users/roles; each user's roles are embedded in the JWT.

## 1. Create the Organization (→ tenant)

Create an organization in your OIDC provider. Record its **Organization ID / name**
— newhub maps it to a tenant via the configurable `OIDC_CLAIM_ORG_ID` claim
(Step 4). The physical column is `tenants.zitadel_org_id` (rename pending a
reserved migration; see migration-ledger).

## 2. Create the OIDC Application (client)

Create an OIDC application (Web confidential, or Native/PKCE for desktop). Set:

| Setting | Value |
|---------|-------|
| Auth Method | `PKCE` (recommended) or confidential (client secret) |
| Access / ID Token | signed RS256 JWT |
| Access Token Lifetime | `3600s` (1h) |
| Refresh Token | enabled (idle 30d / max 90d, typical) |

- **Redirect URIs**: prod `https://api.lurus.cn/api/v2/oauth/callback` · dev `http://localhost:8850/api/v2/oauth/callback`
- **Post Logout Redirect URIs**: `https://api.lurus.cn/logout` · `http://localhost:8850/logout`
- **Grant Types**: Authorization Code + Refresh Token
- **Response Types**: Code

On create, record the **Client ID** and (for confidential clients) **Client Secret**
— the secret is typically shown once; save it immediately.

## 3. Roles

Create the roles newhub recognises. They are surfaced in the token under the
configurable `OIDC_CLAIM_ROLES` claim and flattened by `extractRoles` (accepts
both a role-name-keyed map and a flat array/string-valued map):

| Key | Description |
|-----|-------------|
| `admin` | Tenant administrator with full access |
| `user` | Regular user with basic access |
| `billing_manager` | Billing and subscription management access |

Assign roles to each user in the provider console.

## 4. Map provider claims to newhub (configurable claim keys)

newhub reads tenant/role information from the token under **configurable** claim
keys (defaults are vendor-neutral). Set these to whatever your provider actually
advertises — they may be vendor-prefixed URNs:

```bash
# Defaults (vendor-neutral). Override per provider if it uses different keys.
OIDC_CLAIM_ORG_ID=org_id
OIDC_CLAIM_ORG_DOMAIN=org_domain
OIDC_CLAIM_RESOURCE_OWNER_ID=resource_owner_id
OIDC_CLAIM_RESOURCE_OWNER_NAME=resource_owner_name
OIDC_CLAIM_ROLES=roles
```

## 5. SMTP (transactional email, optional)

If your provider sends verification/notification email, point it at the cluster
mailer (e.g. `mail.lurus.cn:587`, TLS, `noreply@lurus.cn`). On failure, check the
mailer (`kubectl get pods -n mail`, `kubectl logs -n mail deployment/stalwart-mail --tail=50`).

## 6. Configuration / Verification

Prefer resolving endpoints from the provider **discovery** document — newhub then
needs only the issuer + jwks_uri:

```bash
curl <OIDC_ISSUER>/.well-known/openid-configuration | jq \
  '{issuer, authorization_endpoint, token_endpoint, userinfo_endpoint, jwks_uri, end_session_endpoint}'
```

Environment variables (full set + multi-issuer/audience options in
`2b-svc-newhub/CLAUDE.md` § OIDC and `.env.oidc.example`):

```bash
OIDC_ENABLED=true
OIDC_ISSUER=https://<your-idp-host>             # the iss claim value
OIDC_CLIENT_ID=<client id>                       # also the expected audience
OIDC_CLIENT_SECRET=<actual secret>               # omit for public/PKCE clients
OIDC_REDIRECT_URI=https://api.lurus.cn/api/v2/oauth/callback
OIDC_JWKS_URI=<discovery jwks_uri>               # provider JWKS endpoint
# Optional endpoint path overrides (else defaults / discovery are used):
#OIDC_AUTHORIZE_PATH=/oauth/v2/authorize
#OIDC_TOKEN_PATH=/oauth/v2/token
#OIDC_END_SESSION_PATH=/oidc/v1/end_session
# Configurable claim keys: see Step 4.
```

Test the OAuth flow by visiting the provider authorize endpoint with your
`client_id` + `redirect_uri` + `response_type=code` + `scope=openid email profile`.
Expect: redirect to the provider login → after login, redirect to the callback URL.

Troubleshooting: `doc/oidc-troubleshooting.md`. Tenant onboarding (org→tenant→user
mapping): `doc/runbook/tenant-onboarding.md`.
