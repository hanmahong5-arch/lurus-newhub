[中文](./README.zh-CN.md) | English

# Lurus Hub

A multi-tenant LLM gateway: one API in front of 30+ model providers, with per-tenant isolation, usage analytics, and optional billing integration.

## What it is

Lurus Hub (module `lurus-hub`, repository `lurus-newhub`) is a customized derivative of the [New API](https://github.com/QuantumNous/new-api) relay, itself descended from [One API](https://github.com/songquanpeng/one-api). On top of the relay it adds a data-processing layer: per-channel scoring, usage aggregation, multi-tenant OIDC auth, and an optional gRPC hook for reporting usage to an external billing service.

It runs as the production LLM gateway behind Lurus's own products (`hub.lurus.cn`), so the relay path, multi-tenant routing, and V1/V2 REST API are exercised continuously. Some capabilities ship disabled by default and are opt-in per deployment: Meilisearch log search (`MEILISEARCH_ENABLED=false`), OpenTelemetry tracing (`OTEL_TRACING_ENABLED=false`), OIDC login (`OIDC_ENABLED=false`), and the external billing integration (`BILLING_UNIFIED_ENABLED=false`). Treat those as available-but-off, not as delivered end-to-end for your deployment until you turn them on and verify.

## Core capabilities

- **Unified multi-provider relay** — OpenAI-compatible endpoints in front of 20+ model providers, with automatic request/response format conversion across three provider API shapes (`internal/adapter/provider/`, `internal/adapter/handler/router/relay-router.go`).
- **Channel scoring and usage aggregation** — the "data hub" layer: weighted channel selection, health scoring, and rolling usage aggregation independent of the relay path itself (`internal/app/hub/channel_scorer.go`, `internal/app/hub/usage_aggregator.go`).
- **Multi-tenant REST API** — `tenant_slug`-scoped V2 API with role-based access (admin/user/billing_manager) plus a V1 single-tenant-compatible surface for existing integrations (`internal/adapter/handler/router/api-v2-router.go`, `api-router.go`).
- **Vendor-neutral OIDC auth** — login against any standards-compliant identity provider via discovery (`.well-known/openid-configuration`); off by default, config in `.env.example` (`OIDC_*`).
- **Optional billing hook** — reports usage over gRPC/HTTP to a companion account-and-billing service; the server runs standalone with its own session auth when this is unset (`internal/pkg/common/identity_grpc_client.go`, `usage_report.go`).
- **Prometheus metrics** — `/metrics` in Prometheus text format, gated by an auth check when the request carries proxy-forwarded headers (`internal/adapter/handler/router/main.go:37,85`; metric definitions in `internal/pkg/metrics/metrics.go`).

## Quick start

```bash
# --- Backend ---
cp .env.example .env                # fill in SQL_DSN and SESSION_SECRET (both required, no default)
go run ./cmd/server                  # listens on :3000 (PORT)

# --- Frontend console (Bun; web/ has its own package.json) ---
cd web && bun install && bun run dev # :5173, proxies API calls to :3000

# --- Tests ---
go test -short ./...                 # unit only, skips integration (testing.Short())
go test -short -race -count=1 -timeout=15m ./...  # CI's required merge gate (.github/workflows/go-ci.yml)
cd web && bun run test && bun run lint && bun run eslint

# --- Docker Compose (self-contained: server + Postgres + Redis) ---
docker-compose up -d                 # http://localhost:3000

# --- Production build ---
CGO_ENABLED=0 go build -ldflags "-s -w -X 'github.com/LurusTech/lurus-hub/internal/pkg/common.Version=$(cat VERSION)'" -o lurus-api ./cmd/server
```

Notes:
- `SQL_DSN` must be `postgres://` or `postgresql://` — any other scheme (or an unset one) refuses to boot; the MySQL and SQLite dev fallbacks have been removed. `REDIS_CONN_STRING` is recommended but optional (falls back to cookie-only sessions, dev only).
- `go.mod` pins `github.com/LurusTech/lurus-proto-go` through a local `replace ... => ../shared/lurus-proto-go` directive. Building outside this monorepo requires that sibling module to be present (see the two-stage `Dockerfile` for how CI stages it in) or the `replace` line removed.
- Never run a single `_test.go` file directly — table-driven tests share package-level fixtures; use `go test ./<package>/...` or `go test ./...`.

## Architecture

```
cmd/server/main.go        # entrypoint
internal/
├── domain/entity/        # GORM models: channel, token, tenant, user, log, pricing, ability, credit pool…
├── app/                  # business logic
│   ├── relay/             # request dispatch across 30+ provider adapters
│   ├── hub/                # ChannelScorer + UsageAggregator (the "data hub" core)
│   └── governance/         # audit trail
├── adapter/
│   ├── handler/           # HTTP controllers + router/ (v1, v2, relay, internal, web)
│   ├── middleware/         # auth, CORS, rate limiting, distributor
│   ├── repo/                # GORM repositories
│   └── provider/            # per-vendor adapters (openai/, claude/, gemini/, aws/, baidu/, …)
├── lifecycle/             # leader election, graceful shutdown, secret rotation
└── pkg/                   # config, logger, metrics, tracing, search, migration, nats, resilience
web/                       # React 18 + Vite + Semi UI console (Bun; 6 locales, see web/src/i18n/locales/)
migrations/                # PostgreSQL SQL migrations (001-020 are legacy record-only baselines, some MySQL-only; 021+ is the live PG-only, idempotent lineage)
deploy/k8s/                # Kubernetes manifests (staging/UAT overlays)
```

## Configuration

Full reference: [`.env.example`](./.env.example). Selected variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `SQL_DSN` | Yes | — | PostgreSQL connection string; non-Postgres DSNs refuse to boot |
| `SESSION_SECRET` | Yes | — | Session signing key; must match across all nodes in a multi-node deployment |
| `REDIS_CONN_STRING` | Recommended | `redis://redis:6379` | Session store + channel cache; cookie-only session fallback if unset |
| `PORT` | No | `3000` | HTTP listen port |
| `GIN_MODE` | No | `debug` | `debug` or `release` |
| `MIGRATIONS_AUTO_RUN` | No | `true` | Run the embedded SQL migration runner on boot |
| `OIDC_ENABLED` | No | `false` | Enable OIDC login; `OIDC_ISSUER`/`OIDC_JWKS_URI`/`OIDC_CLIENT_ID` become required when true |
| `MEILISEARCH_ENABLED` | No | `false` | Full-text log search |
| `IDENTITY_SERVICE_URL` / `IDENTITY_GRPC_ADDR` | No | — | Optional companion billing/identity service (usage reporting, wallet debit) |
| `BILLING_UNIFIED_ENABLED` | No | `false` | Switch to the pre-authorize/freeze/settle billing flow instead of post-hoc debit |
| `OTEL_TRACING_ENABLED` | No | `false` | Export traces via OTLP |
| `METRICS_AUTH_TOKEN` | No | (empty) | Required to read `/metrics` through a reverse proxy that adds forwarding headers; direct connections without such headers are always allowed |

## API overview

| Surface | Path prefix | Notes |
|---|---|---|
| V1 (legacy, single-tenant compat) | `/api/{user,token,channel,redemption,log,data,wallet}/*` | `router/api-router.go` |
| V2 (multi-tenant) | `/api/v2/:tenant_slug/{tokens,projects,channels,logs,redemptions,sessions,models,pricing,billing,chat}/*`, `/api/v2/admin/{tenants,mappings,internal-keys,users,governance}/*` | RBAC (admin/user/billing_manager); `router/api-v2-router.go` |
| Relay (OpenAI-compatible + native provider formats) | `POST /v1/chat/completions`, `/v1/messages`, `/v1/embeddings`, `/v1/images/generations`, `/v1/audio/*`, `/v1/rerank`; `GET /v1/models`, `/v1beta/models` | `router/relay-router.go` |
| Internal (service-to-service) | `/internal/{user,token,quota,balance,currency,log,models,admin}/*` | Requires `X-API-Key` header matched against a scope, not `Authorization: Bearer`; `router/internal-api-router.go` |

Full OpenAPI spec: [`docs/openapi/api-v2.yaml`](./docs/openapi/api-v2.yaml).

## Development conventions

- Test files follow `*_test.go` / `*_integration_test.go` / `*_benchmark_test.go`; test functions are named `Test<Subject>_<Method>_<Behavior>` and prefer table-driven cases (see [`TESTING.md`](./TESTING.md)).
- CI enforces a coverage floor per layer, ratcheted upward as coverage improves: `internal/app/` ≥ 86%, `internal/adapter/repo/` ≥ 77%, `internal/adapter/handler/` ≥ 64% (`.github/workflows/go-ci.yml`).
- `web/` is plain JS, not TypeScript — there is no `typecheck` script; the real frontend gates are `bun run lint` (prettier) and `bun run eslint`.
- Database schema changes ship as new files under `migrations/`, PostgreSQL-only and idempotent from `021_` onward.
- Deployment is GitOps: merging to `main` builds and publishes an image, and a separate reconciler converges the cluster state — this repo does not itself drive `kubectl` (see [`DEPLOY.md`](./DEPLOY.md)).

## Related projects

Within the Lurus platform, this service exposes a dedicated route group, `/api/v2/switch/*` (`router/api-v2-router.go`), consumed by the Switch desktop client (repository `lurus-switch`) for activation-code redemption and channel/token management. The optional billing hook (`IDENTITY_GRPC_ADDR`, above) talks to a companion account-and-billing core service; both integrations are off unless explicitly configured, so this repository is fully usable standalone.

## License and upstream attribution

Licensed under the terms in [`LICENSE`](./LICENSE): **AGPLv3 by default**, with a **commercial license** required for scenarios such as removing upstream branding or avoiding the AGPLv3 network-source-disclosure obligation (see the file for the full scenario list). This licensing model, including the additional branding-retention restriction under the open-source tier, is inherited from the upstream project.

This project is a customized derivative of:
- [New API](https://github.com/QuantumNous/new-api) (AGPLv3, dual-licensed) — the direct upstream base this project tracks and cherry-picks from.
- [One API](https://github.com/songquanpeng/one-api) (MIT) — the earlier project New API itself derives from.

See [`NOTICE`](./NOTICE) for a summary of bundled third-party component licenses (Go modules and `web/` frontend packages).
