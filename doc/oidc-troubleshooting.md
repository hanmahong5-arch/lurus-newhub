# OIDC Troubleshooting & Fix Guide

> Scope: newhub as an OIDC client/resource server. Covers the failures that
> surface in newhub when the upstream OIDC provider misbehaves. Provider-internal
> operations (admin reset, instance bootstrap) live in the provider's own runbook,
> not here. Vendor-neutral — examples use placeholders; instance values are
> deploy-time and owner-gated.

## Symptom → likely cause

| Symptom (newhub side) | Likely cause | Where to look |
|-----------------------|--------------|---------------|
| All logins 401 "Invalid issuer" | `OIDC_ISSUER` ≠ the token `iss` | compare env to a decoded token's `iss`; multi-issuer env accepts a comma list during migration |
| All logins 401, JWKS "no valid RSA keys" / empty | provider JWKS endpoint returned `{"keys":[]}` (key expired / not rotated) | `curl <OIDC_JWKS_URI>` should list RSA keys |
| "User identity mapping failed" 500 | org/tenant claim missing or under the wrong key | verify `OIDC_CLAIM_ORG_ID` matches the provider's actual claim key |
| Roles empty / RBAC denies | roles claim key mismatch or unexpected shape | verify `OIDC_CLAIM_ROLES`; `extractRoles` accepts a role-name-keyed map or a flat array/string-valued map |
| `redirect_uri_mismatch` at the provider | callback URL not registered on the client | register `OIDC_REDIRECT_URI` exactly on the provider app |

## Diagnostic steps (newhub side)

```bash
# 1. Confirm newhub initialized OIDC (look for the init log line)
kubectl logs -n lurus-system -l app=lurus-api --tail=200 | grep -i "OIDC\|JWKS"
#   Expect: "OIDC authentication initialized successfully"
#           "Successfully refreshed N JWKS keys"

# 2. Verify the provider discovery doc + JWKS are reachable from the pod
kubectl exec -n lurus-system <pod> -- \
  curl -s <OIDC_ISSUER>/.well-known/openid-configuration | jq '{issuer, jwks_uri}'
kubectl exec -n lurus-system <pod> -- curl -s <OIDC_JWKS_URI> | jq '.keys | length'
#   Expect: a non-zero RSA key count.

# 3. Decode a failing token (no verification) to inspect iss/aud/claims
#   Paste the JWT payload into any decoder; check iss == OIDC_ISSUER,
#   aud contains OIDC_CLIENT_ID, and the org/roles claim keys.
```

## Common fixes

1. **Issuer mismatch.** Set `OIDC_ISSUER` to the exact `iss` string the provider
   emits (scheme + host, no trailing slash unless the provider includes one).
   During an IdP migration, set both old and new issuer comma-separated so tokens
   from either are accepted until the old one is retired (≥90d alias window).

2. **Empty / stale JWKS.** If `<OIDC_JWKS_URI>` returns no keys, the provider's
   signing key rotation is broken — fix it on the provider side. newhub
   auto-refreshes JWKS hourly and on key-not-found (rate-limited to once / 30s).

3. **Claim mapping.** If tenant resolution or roles fail, the provider is
   advertising the org/role claims under different keys than the defaults.
   Point `OIDC_CLAIM_*` at the real keys (they may be vendor-prefixed URNs).

4. **Audience.** newhub treats `OIDC_CLIENT_ID` as the expected audience for ID
   tokens. Ensure the provider includes the client id in `aud`.

## History (Zitadel-era, retained for reference)

> Kept as history for the period when the provider was Zitadel (pre IdP
> migration). No longer operationally relevant once a different OIDC provider is
> in use; do not act on the version-specific steps below against a new provider.

### 2026-03-06: provider JWKS key-rotation incident

A low-traffic provider instance let its signing key expire without
auto-rotation; `/oauth/v2/keys` returned empty `{"keys":[]}` and all OIDC logins
failed. Resolution at the time was a provider upgrade to a build with manually
managed signing keys (no auto-expiry). The newhub-side signal was the absence of
the `Successfully refreshed N JWKS keys` log line — the same diagnostic as Step 1
above. DB backup of the provider was taken before the upgrade.
