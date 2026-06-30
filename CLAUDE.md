# Lurus Newhub (2b-svc-newhub)

> **[STATUS 2026-05-12]**: 本目录是 `2b-svc-newhub`（domain: `hub.lurus.cn`，stage on R6: `test-newhub.lurus.cn`）。
> 原 `2b-svc-api`（lurus-hub）已于 **2026-04-23 REMOVED**，由 newapi 接管直接中转职能。

AI 数据处理枢纽 — Platform 产品组核心成员。在 New API 开源基座上进化：数据处理管道 + 个性化定制中转 + 企业级计费集成。多租户 Hub 层（newapi 之上 + Platform 计费），详见 `doc/` 和 `_bmad-output/`。

**与 NewAPI 的关系**: lurus-newapi 是稳定开源中转基座（定期同步上游），Hub 在其上增加数据处理能力和公司定制逻辑。

- **Module**: `github.com/LurusTech/lurus-hub`
- **Namespace / Port**: `lurus-system` / pod:3000, svc:8850
- **Image**: `ghcr.io/LurusTech/lurus-api:main` (runtime resource name preserved)
- **DB**: PostgreSQL only（`lurus_api` schema，GORM auto-migrate + embedded migration runner；非 postgres:// DSN boot fast-fail，2026-06 起），Redis DB 0, Meilisearch (optional)
- **Auth**: OIDC (vendor-neutral; issuer/clientId deploy-time owner-gated), Passkey, session cookie/Redis
- **Product Group**: Platform (P0)

## Core Capabilities

| Capability | Description |
|------------|-------------|
| **Data Processing** | Real-time LLM usage analytics, cost optimization, model performance monitoring |
| **Custom Relay** | Per-product routing rules, per-tenant model pools, quota enforcement |
| **Billing Integration** | Usage metering → Platform billing (gRPC ReportUsage + WalletDebit) |
| **Multi-tenant** | Channel/Token/quota/log isolation per tenant |
| **Upstream Sync** | Cherry-pick New API upstream fixes monthly; security patches immediately |

## Tech Stack

| Layer | Tech |
|-------|------|
| Backend | Go 1.25.1, Gin, GORM |
| Frontend | React 18, Vite, Semi UI (`web/`), Bun |
| DB | PostgreSQL（runtime 唯一；glebarez SQLite 仅 hermetic 单测 tier） |
| Cache | Redis DB 0 (session + channel cache + quota sync) |
| Search | Meilisearch (log full-text, 可选) |
| Observability | Prometheus `/metrics` (Netdata go.d 主动抓，**禁为换栈改业务代码**) |
| Providers | 30+ LLM vendors（`internal/adapter/provider/<vendor>/`：openai/claude/gemini/aws/baidu/cohere/zhipu/… 详见目录） |

## Directory Structure

```
cmd/server/main.go           # Entry point
internal/
├── domain/entity/           # Domain entities (channel, user, log, token, tenant, task…)
├── app/                     # Business logic (relay/, passkey/, billing, quota, channel…)
│   └── relay/               # Multi-modal request dispatch (30+ providers)
├── adapter/
│   ├── handler/             # HTTP controllers + router/ (v1/v2/relay/internal/web)
│   ├── middleware/          # Auth, CORS, rate-limit, distributor, stats
│   ├── repo/                # GORM repositories (channel, user, token, tenant, log…)
│   └── provider/            # AI vendor adapters (openai, claude, gemini, aws, +18 more)
├── lifecycle/               # Background task lifecycle manager
└── pkg/                     # Shared: config, common, constant, dto, types, logger, metrics, search, setting, tracing
web/                         # React frontend (Bun)
migrations/                  # PostgreSQL SQL migrations
deploy/k8s/                  # K8s manifests + staging overlay
```

## Commands

```bash
# --- Local Dev ---
cp .env.example .env                        # 复制并填写 SQL_DSN, REDIS_CONN_STRING, SESSION_SECRET
go run ./cmd/server                         # 后端 port 3000
cd web && bun install && bun run dev        # 前端 port 5173 (代理到 3000)

# --- Build (production) ---
CGO_ENABLED=0 go build -ldflags "-s -w -X 'github.com/LurusTech/lurus-hub/internal/pkg/common.Version=$(cat VERSION)'" -o lurus-api ./cmd/server

# --- Frontend ---
cd web && bun run typecheck
cd web && bun run lint
cd web && bun run build

# --- Test ---
go test -short ./...                        # 单元测试（跳过集成测试）
go test -v -race ./...                      # 全量 + 竞态检测（merge 前必跑）
go test -v ./internal/adapter/handler/...  # 指定包
go test -run Integration ./...             # 仅集成测试
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# --- K8s ---
ssh root@100.98.57.55 "kubectl get pods -n lurus-system"
ssh root@100.98.57.55 "kubectl rollout restart deployment/lurus-api -n lurus-system"
ssh root@100.98.57.55 "kubectl logs -n lurus-system -l app=lurus-api --tail=100"
ssh root@100.98.57.55 "kubectl describe pod -n lurus-system <pod>"
```

## K8s Deployment Facts

| Key | Value |
|-----|-------|
| nodeSelector | `lurus.cn/vpn: "true"` |
| Resources | req: 256Mi/100m  lim: 1Gi/500m |
| Security | runAsUser:65534, readOnlyRootFilesystem, drop ALL caps |
| Volumes | `data: emptyDir`, `tmp: emptyDir` (no persistent disk) |
| Redis | `redis://redis:6379` (in-cluster) |
| Meilisearch | `http://meilisearch:7700` (in-cluster) |
| Outbound proxy | `http://10.42.1.1:10808` (for Gemini/OpenAI/外网 LLM) |
| NO_PROXY | `*.svc,*.svc.cluster.local,*.lurus.cn,10.0.0.0/8,…` |
| ALLOWED_ORIGINS | `https://www.lurus.cn,https://lucrum.lurus.cn` |
| MODEL_SYNC_FREQUENCY | `60` (分钟) |
| Secret | `lurus-api-secrets` (SESSION_SECRET, SQL_DSN, OIDC_CLIENT_ID, IDENTITY_SESSION_SECRET, IDENTITY_SERVICE_INTERNAL_KEY, ALIPAY_*) |

## Environment Variables

env 详表（Required / platform Integration / OIDC / Meilisearch / Runtime Tuning / Observability / Proxy / OAuth）见 **`.env.example`** 与 **`doc/claude-md-archive-2026-06-10.md`**。
⚠️ Observability: 监控栈已切 **Netdata 自托管**（lurus/CLAUDE.md HARD RULE）；旧 OTLP→jaeger-collector...:4318 collector 已停。服务继续暴露 Prometheus-format `/metrics` 由 go.d `prometheus` collector 主动抓，**禁为换栈改业务代码**。

## Internal API Scopes

路径前缀 `/internal`，需 `Authorization: Bearer <key>` + scope 匹配（`repo.ScopeXxx`）。

| Scope | Endpoints |
|-------|-----------|
| `ScopeUserRead` | `GET /internal/user/:id`, `/user/by-email/:email`, `/user/by-phone/:phone` |
| `ScopeUserWrite` | `PUT /internal/user/:id` |
| `ScopeQuotaRead` | `GET /internal/quota/user/:id` |
| `ScopeQuotaWrite` | `POST /internal/quota/adjust` |
| `ScopeBalanceRead` | `GET /internal/balance/user/:id` |
| `ScopeBalanceWrite` | `POST /internal/balance/topup` |

> Route Groups / gRPC Integration / Relay Formats 全表移至 `doc/claude-md-archive-2026-06-10.md`；契约真源 = `lurus/doc/coord/contracts.md`。

## Key Runtime Notes

- **DB 自动迁移**: 启动时 GORM 自动建表（Go 结构体驱动）+ embedded SQL migration runner（`internal/pkg/migration`，boot lease winner 上自动跑，`MIGRATIONS_AUTO_RUN=false` 可关）。**Baseline 契约**: `migrations/` 001–020 只记账永不执行（001–004 是 MySQL 方言）；021 起必须 PG-only + 幂等，ID 先在 root `doc/coord/migration-ledger.md` 预留
- **PG-only (2026-06)**: `SQL_DSN` 必须 `postgres://`/`postgresql://`，否则 boot fast-fail；MySQL 与 SQLite dev fallback 已删（glebarez SQLite 仅存于 hermetic 单测 tier）
- **渠道缓存**: Redis 存在时自动启用内存缓存，`SYNC_FREQUENCY` 控制同步周期
- **Background tasks**: `lifecycle.Manager` 管理，`TickerTask` 封装定时任务
- **ProtoImport**: 通过独立模块 `lurus-proto-go` 引用 identity gRPC 契约类型（`github.com/LurusTech/lurus-proto-go/identity/v1`）
- **go.mod replace**: `github.com/LurusTech/lurus-proto-go => ../shared/lurus-proto-go`（本地开发；发布到 GitHub 后移除）

## BMAD

| Resource | Path |
|----------|------|
| PRD | `./_bmad-output/planning-artifacts/prd.md` |
| Epics | `./_bmad-output/planning-artifacts/epics.md` |
| Architecture | `./_bmad-output/planning-artifacts/architecture.md` |
| Sprint Status | `./_bmad-output/planning-artifacts/sprint-status.yaml` |
| Project Context | `./_bmad-output/planning-artifacts/project-context.md` |

**Story 文档规则（Epic 6+ 严格执行）**: 实现前建 story 文档 → 通过 `dev-story/checklist.md` → 含验证证据才可标 done。违反 = 工作无效。

---
_BMAD artifacts last review: 2026-05-18 — governance: `lurus/doc/audit/2026-05-18-bmad-output-stale.md`._
