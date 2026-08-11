# Runbook — turning `OIDC_ENABLED` on (blast radius + order of operations)

> **Source**: Lutu APP web search returns 503 in PROD and STAGE.
> **Triggered by**: `POST /api/v2/lutu/search` → `503 {"message":"OIDC authentication is not enabled"}`.
> **Severity**: product feature dark (client degrades silently — the user sees
> no search results and no error).
> **Last review**: 2026-08-08 (read at HEAD; the flag was still off in both envs).

## Why this needs a runbook

`OIDC_ENABLED` is a single global switch, but it does **not** gate a single
feature. Four independent auth paths read it, and two of them change behaviour
in ways that are not obvious from the flag's name. Flipping it to fix Lutu
search without reading this list can lock out an admin client.

## What the flag actually controls

| Path | `OIDC_ENABLED=false` (today) | `OIDC_ENABLED=true` |
|---|---|---|
| `RequireOIDCToken` → `POST /api/v2/lutu/search` | 503 for everyone (feature dark) | JWT verified (signature + issuer + audience); valid consumer tokens pass |
| `OIDCAuth` → `/api/v2/user/**`, `/api/v2/:tenant_slug/credit-pool/me` | 503 for everyone | JWT verified + mapped to a newhub tenant/user |
| `RootJWTAuth` → `/api/v2/admin/**` | **no Authorization header** → session auth; **with a header** → *also* session auth (silent fallback) | **no header** → session auth (unchanged); **with a header** → the header MUST be a valid OIDC JWT carrying the `root` role, else 401/403 |
| `resolveOIDCAccountID` (release download gate) | never resolves an account from a bearer token | resolves the account, so the gate can attribute a download |

### The one that bites: `/api/v2/admin/**`

`RootJWTAuth` (`internal/adapter/middleware/admin_jwt_auth.go:67`) falls back to
session auth whenever OIDC is off — *even when the caller sent an
`Authorization` header*. Any admin client that today sends a header which is not
an OIDC JWT (an old newhub API key, a stale token, a copy-pasted value) is
currently succeeding **through the session cookie**, with the header ignored.
The moment OIDC is on, that same request is validated as a JWT and rejected.

**Check before flipping**, on a node with cluster access:

```bash
# Any non-JWT bearer reaching the admin group? A JWT has two dots and starts "ey".
kubectl logs -n lurus-newhub deploy/lurus-newhub --since=168h \
  | grep -E '"path":"/api/v2/admin' | grep -v '"authorization":"Bearer ey'
```

Empty output = no client relies on the fallback; flipping is safe for admin.
Non-empty = fix those callers first (use the session cookie, or issue them a
real OIDC token), otherwise they 401 at flip time.

## Order of operations

1. **Confirm the IdP side is real.** `curl -s "$OIDC_ISSUER/.well-known/openid-configuration"`
   must return 200 and its `issuer` field must equal `OIDC_ISSUER` exactly. A
   mismatch here means every token is rejected the moment the flag goes on.
2. **Set the audience allow-list to observe-only** (this is the default, so it
   normally means "do nothing"): leave `OIDC_CONSUMER_AUD_REQUIRED` unset or
   `log`. Do **not** set `enforce` yet — see the next section.
3. **Flip `OIDC_ENABLED=true`** and restart. Startup fast-fails when
   `OIDC_ISSUER` / `OIDC_JWKS_URI` / `OIDC_CLIENT_ID` are missing, so a
   half-configured deploy will not come up rather than come up unauthenticated.
4. **Verify the feature is alive**: a Lutu APP login → `POST /api/v2/lutu/search`
   returns 200 rather than 503.
5. **Verify nothing else broke**: `/api/v2/admin/**` still reachable from the
   web console (session path), and `/api/v2/user/**` returns 200/401 rather
   than 503.

**Rollback** is `OIDC_ENABLED=false` + restart. It is complete and immediate:
every path above returns to the left-hand column.

## Then, and only then: enforce the consumer audience

The consumer gate accepts a token whose `aud` matches `OIDC_CLIENT_ID`,
`OIDC_ALLOWED_AUDIENCES`, or `OIDC_CONSUMER_AUDIENCES`. A first-party consumer
app holds **its own** client id, so its tokens carry neither this service's
client id nor anything else you have configured yet — which is exactly why the
check ships in `log` mode.

1. With the flag at `log`, watch for a week:
   ```
   consumer_audience_mismatch_total{action="log"}
   ```
   and the matching `consumer gate audience mismatch (admitted; ...)` SysLog
   lines, which print the actual `aud` values.
2. Put the audiences you see — and can account for — into
   `OIDC_CONSUMER_AUDIENCES` (comma-separated). The Lutu APP's client id is in
   `2c-app-lutu/lib/constants.dart` (`OidcConfig.clientId`).
3. Restart, confirm the counter stops rising.
4. Set `OIDC_CONSUMER_AUD_REQUIRED=enforce`.

🔴 **Enforcing before step 2 rejects every consumer caller** — the allow-list
starts empty and no legitimate consumer token carries newhub's own client id.
An unrecognized flag value degrades to `log`, never to `enforce`, so a typo
cannot cause this; a deliberate early `enforce` can.

## What this does not fix

Turning the flag on does not populate `OIDC_ISSUER` with both the old and the
new issuer during the `auth.lurus.cn` → `identity.lurus.cn` alias window. That
env accepts a comma-separated list for exactly this reason — see
`lurus/doc/coord/issuer-cutover.md` and the contract's deploy order (consumers
accept both issuers → IdP flips → drop the old entry).
