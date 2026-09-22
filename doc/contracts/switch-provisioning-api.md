# Switch Provisioning API Contract

**Version**: v0.1.0 (draft)
**Service**: `2b-svc-newhub` (hub.lurus.cn) — STAGE: `test-newhub.lurus.cn` on R6
**Status**: Draft for Switch team integration review
**Last updated**: 2026-05-18 (Lane ε / Wave 1 Phase 2)
**Source ADR**: `_bmad-output/planning-artifacts/adr-2026-05-18-tenant-credit-pool.md` §4.2 (canonical)

---

## 1. Purpose

Extracts the Provisioning API contract from the canonical ADR §4.2 into a Switch-team-facing spec so `2c-gui-switch` can integrate without reading the whole ADR. **Canonicality**: ADR wins on disagreement — file a PR to re-sync, don't implement against this doc alone if divergence affects behaviour. **Scope**: Reseller-driven programmatic key create/revoke only. Pool management (create/topup/usage) lives on session-authenticated `v2/admin` (ADR §4.1), NOT a Switch target.

---

## 2. Authentication

> **🔴 Header**: requires `X-API-Key: <management-key>`, NOT `Authorization: Bearer`. Matches newhub `internal_api_auth.go` middleware (shared by all `/internal/*`). Sending Bearer → 401 `{"success":false,"message":"API key required"}` (middleware reads `X-API-Key` only).

- **2.1 Issuing**: a management key is an `internal_api_keys` row with scope `provisioning:write`; issuance is an operator action on newhub (see ADR §9 Q2 for 90-day TTL + T-14d/T-7d/T-1d rotation alert schedule). Switch receives the key out-of-band (1Password / encrypted channel).
- **2.2 Rotation**: 90-day TTL (ADR §9 Q2), operator-driven via NATS alerts at T-14d/T-7d/T-1d before `expires_at`; rotated key has a fresh value, previous revoked atomically. Switch treats any 401 on a previously-working key as "rotated — fetch new key from secret store".
- **2.3 Scope**: `provisioning:write` grants create/revoke tokens within any tenant + read slug→tenant-ID (implicit in `:slug`). Does NOT grant other internal scopes (`balance`/`quota`/`user`), platform credentials, wallet, or Reseller session. Blast radius bounded to one tenant per call (`:slug`).

---

## 3. Endpoints

### 3.1 Create a provisioned key

```
POST /internal/v1/provisioning/tenants/:slug/keys
```

**Headers**:

```
X-API-Key: lurus_ik_<management-key>
Content-Type: application/json
```

**Path params**:
- `:slug` — tenant slug (URL-safe identifier; not the numeric tenant ID).

**Request body**:

```json
{
  "name": "acme-prod-key-2026-q3",
  "limit": 50000000,
  "limit_reset": "monthly",
  "expires_at": "2026-12-31T23:59:59Z",
  "model_allowlist": ["gpt-4o", "claude-sonnet-4-5"],
  "allow_ips": ["203.0.113.0/24"]
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `name` | string | yes | Human label for audit + UI display. Unique within tenant (see §4 idempotency). |
| `limit` | int64 \| null | no | Per-key quota ceiling in lurus-units. `null` = unlimited. |
| `limit_reset` | string | no | One of `"none"`, `"daily"`, `"weekly"`, `"monthly"`. Default: `"none"`. |
| `expires_at` | string \| null | no | ISO 8601 UTC. `null` = no expiry. |
| `model_allowlist` | string[] | no | Empty array = all models allowed (mirrors OpenRouter `model_whitelist`). |
| `allow_ips` | string[] | no | CIDR or single IPs. Empty = no IP restriction. |

**Success response** (HTTP 200):

```json
{
  "id": 12345,
  "key": "sk-lurus-abcdef0123456789...",
  "name": "acme-prod-key-2026-q3",
  "creator_user_id": 42,
  "limit": 50000000,
  "limit_reset": "monthly",
  "expires_at": "2026-12-31T23:59:59Z",
  "created_at": "2026-05-18T11:23:45Z"
}
```

> **Key visibility**: the `key` field is the only time the full token
> value is returned. Switch team MUST persist it on the same response
> turn — newhub does not store the raw key (only the hashed form). Lost
> keys cannot be recovered; the only remedy is revoke + recreate.

**Error responses**:

| Status | Body shape | Cause |
|--------|------------|-------|
| 401 | `{"success": false, "message": "API key required"}` | Missing `X-API-Key` header |
| 401 | `{"success": false, "message": "Invalid or expired API key"}` | Wrong / rotated / expired management key |
| 403 | `{"success": false, "message": "Insufficient permissions. Required scope: provisioning:write"}` | Key valid but lacks scope |
| 404 | `{"success": false, "message": "Tenant not found: <slug>"}` | Slug does not exist |
| 409 | `{"success": false, "message": "Token name collision within tenant"}` | Name already in use (see §4 idempotency) |
| 422 | `{"success": false, "message": "Invalid model in allowlist: <model>"}` | Model not registered in newhub |
| 422 | `{"success": false, "message": "Invalid limit_reset: <value>"}` | Not in {none, daily, weekly, monthly} |

**curl example**:

```bash
curl -X POST \
  https://hub.lurus.cn/internal/v1/provisioning/tenants/acme-corp/keys \
  -H "X-API-Key: lurus_ik_REPLACE_ME" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "acme-prod-key-2026-q3",
    "limit": 50000000,
    "limit_reset": "monthly",
    "expires_at": "2026-12-31T23:59:59Z",
    "model_allowlist": ["gpt-4o", "claude-sonnet-4-5"],
    "allow_ips": []
  }'
```

### 3.2 Revoke a provisioned key

```
DELETE /internal/v1/provisioning/tenants/:slug/keys/:key_id
```

**Headers**:

```
X-API-Key: lurus_ik_<management-key>
```

**Path params**:
- `:slug` — tenant slug.
- `:key_id` — numeric `id` returned by the create call (NOT the raw key string).

**Success response**: HTTP 204 No Content (empty body).

**Error responses**:

| Status | Cause |
|--------|-------|
| 401 | Missing / invalid `X-API-Key` |
| 403 | Scope `provisioning:write` not granted |
| 404 | Slug not found, or `key_id` does not belong to that tenant |

**curl example**:

```bash
curl -X DELETE \
  https://hub.lurus.cn/internal/v1/provisioning/tenants/acme-corp/keys/12345 \
  -H "X-API-Key: lurus_ik_REPLACE_ME" \
  -i
```

Revocation is immediate: the key's `status` flips to disabled in the
`tokens` table, in-flight relays still complete (no mid-stream kill), but
new requests using that key return 401 within one cache-sync cycle
(default 60s; see `SYNC_FREQUENCY` env).

---

## 4. Idempotency

The Provisioning API is **best-effort idempotent by `name` within a tenant**:

- Create with a `name` that already exists → 409 (do not auto-return the
  existing key — the raw key value is not retrievable).
- Switch team's integration should:
  1. Generate a deterministic `name` per (tenant, key-purpose) tuple
     (e.g., `acme-corp:prod-relay`).
  2. On retry after network failure, attempt create; on 409, assume the
     previous attempt succeeded — query the admin surface or persist
     the response from the successful turn.
- Revoke with a non-existent `key_id` → 404. Treat as success (the key
  is gone either way).

> **Known gap (Q4 improvement)**: the current schema does not store a
> client-provided idempotency key. A retry that races with the original
> within the same TCP timeout window CAN produce two tokens with
> different `id` values but the same `name` — the second wins the
> uniqueness check by losing (409). This is acceptable for the manual
> + 1-key-per-tenant onboarding flow targeted in v0.1.0, but should be
> hardened with a proper `Idempotency-Key` header before scaling to
> high-volume programmatic provisioning. Track as Q4 follow-up.

---

## 5. Version Lock

`v0.1.0`, semver-for-APIs: **Patch** (`v0.1.x`, doc clarifications / extra error codes / new optional response fields) backwards-compatible, no coordination; **Minor** (`v0.2.0`, new optional request fields / new endpoints in `/internal/v1/provisioning/`) backwards-compatible, notify Switch; **Major** (`v1.0.0`+, breaking: request schema / status codes / path / auth header) requires Switch coordination + coordinated cutover.

Breaking change (v0.1.0→v0.2.0): bump version header here + canonical ADR; file `2c-gui-switch` issue describing migration; hold both contracts active ≥7 days during cutover.

---

## 6. Switch Integration Handoff

Consumer: `2c-gui-switch` (Wails desktop, Go + TS). Surface: Switch end-users in Reseller mode provision keys for EndUser tenants; the management key is held by the Reseller desktop instance (encrypted at rest via Wails keychain); each session calls `POST .../tenants/:slug/keys` on EndUser onboarding, presents the returned `key` once for copy-paste, then discards from memory.

File issues: contract bugs (doc out of sync) → `hanmahong5-arch/lurus-newhub` label `contract:provisioning`; Switch-side bugs → `2c-gui-switch`; cross-cutting (e.g. key-rotation UX) → root governance repo, both labels.

STAGE target: `test-newhub.lurus.cn` (R6); management key provisioned via operator session, rotated 90d (ADR §9 Q2). Connectivity check: `GET https://hub.lurus.cn/api/status` → `{"success":true}` before `POST /internal/v1/provisioning/...`.

---

## 7. Out of Scope (Phase 2) — track for v0.2.0+

1. **Bulk key creation** (today one call per key). 2. **Key listing** (`GET /internal/v1/provisioning/tenants/:slug/keys` without raw-key disclosure — Switch UI uses v2/admin session surface for now). 3. **Programmatic management-key rotation** (currently operator action, ADR §9 Q2). 4. **Idempotency-Key header** (§4 known gap). 5. **Webhook callbacks** (key-usage/threshold events route through NATS `LLM_EVENTS` ADR §9 Q5, not provisioning-API webhooks).

---

## 8. References

- **Canonical source**: `_bmad-output/planning-artifacts/adr-2026-05-18-tenant-credit-pool.md` §4.2
- **Middleware**: `internal/adapter/middleware/internal_api_auth.go`
- **Companion**: `_bmad-output/planning-artifacts/adr-2026-05-18-budget-alerts.md` (alert event taxonomy)
- **Runbook**: `doc/runbook/pool-threshold-alert.md` (operator triage on `CreditPoolBalanceLow`)
- **Consumer**: `2c-gui-switch` (Wails desktop, Reseller mode integration)
