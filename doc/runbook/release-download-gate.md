# Runbook — Release Download Entitlement Gate

> ADR-0017 Step 1 — close the "anonymous can download a paid artifact" hole on
> `GET /api/releases/:id/download/:artifact_id`.
> Status: **mechanism shipped, default OFF**. Activation is owner/ops-gated.

## What shipped

`middleware.ReleaseDownloadGate()` is now chained on the download route
(`internal/adapter/handler/router/api-router.go`). Behaviour:

| Situation | Result |
|---|---|
| `RELEASE_GATED_PRODUCTS` empty (default) | **no-op** — every download stays public, exactly as before. Zero behaviour change on deploy. |
| product **not** in the gated set | public download (unchanged) |
| gated product + anonymous / invalid JWT | **401** `DOWNLOAD_AUTH_REQUIRED` |
| gated product + signed-in but no entitlement | **403** `NO_ENTITLEMENT` |
| gated product + entitlement system unreachable | **403** `ENTITLEMENT_CHECK_FAILED` (fail-CLOSED) |
| gated product + valid entitlement | download proceeds (302 → MinIO) |

Fail-CLOSED is deliberate and is the one place it differs from the LLM-relay
`EntitlementCheck` (which is fail-OPEN because relay quota is also enforced
downstream — a binary download has no second line of defence).

Identity is resolved from a **Zitadel JWT** (`Authorization: Bearer <jwt>`),
reusing the same verify-signature → issuer-check → `UpsertAccountGRPC` path as
`ZitadelAuth`, but non-aborting. `sk-` API tokens do **not** resolve a platform
account id, so a gated product cannot be downloaded with a token alone (by
design, fail-closed).

## How to ACTIVATE (owner / ops)

1. **Decide which products are paid.** Only those need gating. Free / OSS
   products stay out of the list.
2. Set the env var on the deployment (comma-separated `product_id`s, matching
   `releases.product_id`):
   ```
   RELEASE_GATED_PRODUCTS=lurus-switch,lurus-creator
   ```
   (K8s: `RELEASE_GATED_PRODUCTS` is a plain deployment env var, not a
   Secret key — add it to `deploy/k8s/r6-stage/deployment.yaml` — **add,
   never remove** existing env per the cluster three-rules.)
3. Roll the deployment. Startup logs `release download gate ENABLED for products: ...`.
4. Verify:
   ```bash
   # anonymous → 401
   curl -s -o /dev/null -w '%{http_code}\n' https://hub.lurus.cn/api/releases/<gated_id>/download/<artifact_id>
   # signed-in entitled user → 302 (Location: MinIO presigned URL)
   curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer <zitadel_jwt>" \
        https://hub.lurus.cn/api/releases/<gated_id>/download/<artifact_id>
   # ungated product → still 302/404 as before
   ```

## ⚠️ Owner / platform confirmation required before activation

The admit predicate is currently `quota_remaining != 0`
(`checkProductEntitlement` in `release_gate.go`) — the only entitlement
semantic already documented in this codebase (the relay uses the same key).

This means a gated product's platform entitlement **must** surface a
`quota_remaining` value:
- perpetual / one-time licence → platform should report `quota_remaining = -1`
  (treated as unlimited → allowed),
- metered download allowance → `> 0`,
- exhausted / none → `0` or absent → **denied**.

**Confirm with the platform team** that gated products return an appropriate
`quota_remaining` entitlement for `GetEntitlements(accountID, "<product_id>")`.
If the platform models download access with a *different* key (e.g. a boolean
`download_allowed`), update the predicate in `checkProductEntitlement` to read
that key instead — it is the single point to change.

## Rollback

Unset / clear `RELEASE_GATED_PRODUCTS` (or remove a single product from it) and
roll. The gate reverts to a no-op with no code change. Fully reversible.

## Tests

`internal/adapter/middleware/release_gate_test.go` — hermetic table-driven
(no DB / JWKS / platform HTTP; dependencies injected via `ReleaseGateConfig`):
feature-off, ungated-anonymous, gated-anonymous→401, gated-no-entitlement→403,
gated-entitlement-error→403, gated-entitled→pass, malformed-id / not-found→defer.
Run: `go test -run TestReleaseDownloadGate ./internal/adapter/middleware/`.
