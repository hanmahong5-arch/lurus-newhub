# ADR: V1 API Deprecation Plan

**Status**: Accepted (In progress — Phase 1 Announce)
**Date**: 2026-02-03
**Updated**: 2026-10-25
**Relates to**: ADR-API-007 (architecture.md)

## Context

Lurus Hub has dual REST surfaces: v1 (`/api/...`, session-based, single-tenant-era) and v2 (`/api/v2/...`, multi-tenant). Maintaining both increases security audit surface and code complexity.

The OpenAI-compatible **relay** routes (`/v1/chat/completions`, etc. on `relay-router.go`) are **out of scope** for deprecation; they follow an industry-standard path and use independent Token-based auth.

The legacy Semi UI console was retired in favour of `web/next` (#257). Remaining work is migrating console and reseller callers off REST `/api/*`.

## Decision

4-phase deprecation after v2 reaches production parity:

| Phase | Action | Trigger |
|-------|--------|---------|
| Announce | Inject `Deprecation: true` + `Sunset` HTTP headers on v1 responses; metric `lurus_gateway_v1_api_requests_total` | **Done** (2026-10-25) |
| Migration | Frontend switches to v2 endpoints; publish migration guide | In progress |
| Monitor | Weekly reports from the v1 usage counter | Ongoing after Announce |
| Sunset | v1 returns 410 Gone, then remove code | T+ after console + reseller clear |

### Long-lived public `/api` carve-outs (not deprecated)

These boot/status surfaces stay without Deprecation headers until a deliberate v2 public replacement ships:

- `/api/status`, `/api/notice`, `/api/health`, `/api/pricing`, `/api/setup`
- `/api/user-agreement`, `/api/privacy-policy`

### Prerequisites (met)

- v2 API feature-complete for tenant console flows
- OIDC production-verified
- Tenant backfill stable
- E2E coverage on `web/next` against UAT

## Migration progress (2026-10-25)

Moved to v2 in this cycle:

- Account TOTP + `/user/setting` → `/api/v2/~/user/totp/*`, `/api/v2/~/user/setting`
- System options read/write → `/api/v2/admin/options`

Still on deprecated `/api` (headers + metric; migrate next):

- Platform channel admin (`/api/channel/...`) — ops-channels console
- Platform user admin (`/api/user/`, `/api/group/`) — admin-users console
- Ratio sync, log purge, `rest_model_ratio`, Midjourney/task shells as applicable

## Consequences

- (+) Deprecation is observable to HTTP clients and Prometheus
- (+) Account security settings no longer depend on the sunset surface
- (-) External integrations using v1 session auth need the Sunset date (`2026-06-30`) as a planning signal
- (-) Ops channel/user admin pages still emit deprecated traffic until Phase Migration finishes those clients
