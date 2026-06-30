<div align="center">

![lurus-hub](/web/public/logo.png)

# Lurus Hub

**AI Data Processing Hub & Multi-Tenant LLM Gateway · AI 数据处理枢纽 · 多租户大模型网关**

![Go](https://img.shields.io/badge/Go-1.25-blue?logo=go) ![License](https://img.shields.io/badge/License-MIT-brightgreen) ![Meilisearch](https://img.shields.io/badge/Meilisearch-v1.10+-orange?logo=meilisearch) ![Docker](https://img.shields.io/badge/Docker-Ready-blue?logo=docker) ![K3s](https://img.shields.io/badge/K3s-Production-green?logo=kubernetes)

</div>

## Overview / 项目简介

**Lurus Hub** is an AI data processing hub built on top of a multi-tenant LLM relay. Beyond unified API access to every major model provider, it adds real-time usage analytics, cost optimization, per-product routing, and platform-grade billing — turning a relay into a data plane.

基于 [New API](https://github.com/QuantumNous/new-api) / [One API](https://github.com/songquanpeng/one-api) 开源基座深度定制:实时用量分析、成本优化、按产品个性化路由,集成 Meilisearch 搜索、OIDC 多租户认证（厂商中性）、Prometheus/OpenTelemetry 可观测性,与 lurus-platform 通过 gRPC 完成计费打通。

## Core Features

- **Multi-Tenant**: OIDC auth + tenant isolation (shared DB + GORM plugin auto-inject `tenant_id`); V2 API `/api/v2/:tenant_slug/*` with RBAC (admin/user/billing_manager); V1 backward-compat; platform-admin cross-tenant API.
- **AI Gateway**: unified API for OpenAI/Claude/Gemini/DeepSeek/Qwen/GLM/Moonshot/+; format auto-conversion (OpenAI ↔ Claude ↔ Gemini); weighted LB + auto-retry + priority channel selection; embeddings/rerank/TTS/STT/image/video; OpenAI Realtime (WebSocket).
- **Search & Performance**: Meilisearch (<50ms search across logs/users/channels); object pooling (BufferPool/IntSlicePool/MapPool); gateway overhead p95 <50ms (benchmark-verified); HA (2-replica rolling + PDB).
- **Observability**: Prometheus `/metrics` (11 metric types); OpenTelemetry tracing + Jaeger + X-Trace-Id; 10 alerting rules; structured JSON logging (slog).
- **Billing & Security**: per-token/per-request/time-based billing + cache billing; online top-up (Creem/Stripe); model-level permissions + IP whitelist + token quotas; audit logging.

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25, Gin, GORM, PostgreSQL/SQLite |
| Frontend | React 18, Vite, Semi UI, TailwindCSS (Bun) |
| Search / Cache | Meilisearch v1.10+ / Redis |
| Auth | OIDC + JWT (vendor-neutral) |
| Observability | Prometheus, OpenTelemetry, Jaeger |
| Deployment | K3s, ArgoCD (GitOps), Docker; CI/CD: GitHub Actions → GHCR → ArgoCD sync |

## Architecture

```
Lurus Hub Gateway (Hexagonal / Go+Gin)
  ├── V1 API (compat) · V2 API (multi-tenant) · Relay API (/v1/chat/*)
  ├── OIDC · PostgreSQL (tenant_id) · Meilisearch
  ├── Redis cache · Prometheus · Jaeger
  └── upstream: OpenAI/Azure · Claude/DeepSeek · Gemini/Qwen/GLM
```

```
internal/
├── domain/entity/     # Domain models (no deps)
├── app/               # Business logic (relay/, passkey/)
├── adapter/           # handler/ (+ router/ v1,v2,v2-admin), middleware/, repo/, provider/
├── lifecycle/         # init, shutdown, background tasks
└── pkg/               # config, logger, metrics, tracing, search
```

## Quick Start

```bash
# Dev
go build -o lurus-api ./cmd/server && ./lurus-api
cd web && bun install && bun run dev

# Tests (see TESTING.md). IMPORTANT: test by package or ./... — never a single _test.go file (missing deps).
go test ./...                                   # all
go test -short ./...                            # unit only (skip integration)
go test -race ./...                             # race detector (before merge)
go test -v ./internal/app/ -run TestCompareVersions
cd web && bun run test && bun run typecheck && bun run lint

# Docker Compose
docker-compose up -d                            # http://localhost:3000

# Production (K3s + ArgoCD) — see doc/runbook/deployment.md
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o app ./cmd/server
ssh root@100.98.57.55 "kubectl rollout restart deployment/lurus-api -n lurus-system"
```

## API Endpoints

- **V2 Multi-Tenant (OIDC JWT)**: `GET /api/v2/:slug/auth/login`, `GET /api/v2/:slug/user/self`, `CRUD /api/v2/:slug/token/`, `CRUD /api/v2/:slug/channel/`, `GET /api/v2/:slug/log/`, `POST /api/v2/admin/tenants`.
- **Relay (OpenAI-compatible)**: `POST /v1/chat/completions`, `POST /v1/messages` (Claude), `POST /v1/embeddings`, `POST /v1/images/generations`.
- **V1 Legacy**: `POST /api/user/login`, `GET /api/user/self`, `CRUD /api/token/`, `GET /api/log/search` (Meilisearch).

Full API: [docs.lurus.cn](https://docs.lurus.cn/) · [OpenAPI Spec](./docs/openapi/api-v2.yaml) (45 endpoints, 30+ schemas).

## Documentation

| Doc | Description |
|-----|-------------|
| [Deployment Runbook](./doc/runbook/deployment.md) | Build, deploy, verify, rollback |
| [Database Runbook](./doc/runbook/database.md) | Backup, restore, migration |
| [Tenant Onboarding](./doc/runbook/tenant-onboarding.md) | New tenant setup |
| [Incident Response](./doc/runbook/incident-response.md) | Triage, escalation, postmortem |
| [HA Deployment](./doc/runbook/ha-deployment.md) | High availability guide |
| [OIDC Setup](./doc/oidc-setup-guide.md) | OIDC auth configuration (vendor-neutral) |
| [Development Log](./doc/process.md) | Change history |

## Environment Variables

Required: `SQL_DSN` (PostgreSQL), `SESSION_SECRET` (session encryption). Recommended: `REDIS_CONN_STRING`. Optional: `MEILISEARCH_ENABLED`/`MEILISEARCH_HOST`/`MEILISEARCH_API_KEY`, `OIDC_ISSUER`/`OIDC_CLIENT_ID`, `OTEL_TRACING_ENABLED`/`OTEL_EXPORTER_OTLP_ENDPOINT`.

Full config: [.env.meilisearch.example](./.env.meilisearch.example), [.env.oidc.example](./.env.oidc.example), and `2b-svc-newhub/CLAUDE.md` § Environment Variables.

## License

MIT. See [LICENSE](./LICENSE). Based on [One API](https://github.com/songquanpeng/one-api) (MIT).
