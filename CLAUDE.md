# Lurus Newhub (2b-svc-newhub)

> **[STATUS 2026-08-30]**: 本目录是 `2b-svc-newhub`（生产 domain: `hub.lurus.cn`；`test-newhub.lurus.cn` 自 2026-08-30 起指向隔离 UAT 实例,不再是生产别名）。
> 原 `2b-svc-api`（lurus-hub）已于 **2026-04-23 REMOVED**，由 newapi 接管直接中转职能。

AI 数据处理枢纽 — Platform 产品组核心成员。在 New API 开源基座上进化：数据处理管道 + 个性化定制中转 + 企业级计费集成。多租户 Hub 层（newapi 之上 + Platform 计费），详见 `doc/` 和 `_bmad-output/`。

**与 NewAPI 的关系**: lurus-newapi 是稳定开源中转基座（定期同步上游），Hub 在其上增加数据处理能力和公司定制逻辑。

- **Module**: `github.com/LurusTech/lurus-hub`
- **Namespace / Port**: `lurus-newhub`(R6;2026-07-15 live 核实,旧记载 `lurus-system` 已 rot)/ pod:3000, svc:8850(NodePort 30850)
- **Image**: `ghcr.io/hanmahong5-arch/lurus-newhub`(digest 钉版,imagePullPolicy=IfNotPresent;`:main` 由 Publish workflow 更新)
- **DB**: PostgreSQL **16.14**，库 `newhub`，表在 **`public`** schema（2026-08-24 实测 40 张；旧记载的 `lurus_api` schema 已 rot）。GORM auto-migrate + embedded migration runner；非 postgres:// DSN boot fast-fail（2026-06 起）。Redis **DB 2**（`redis.lurus-system.svc:6379/2`，旧记载 DB 0 已 rot）。Meilisearch 未部署（`MEILISEARCH_ENABLED=false`）
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
# NB: web/ has no `typecheck` script (plain JS, not TS) — `bun run typecheck`
# has always failed here. Real gates: lint = prettier --check, plus eslint.
cd web && bun run lint
cd web && bun run eslint
cd web && bun run build

# --- Test ---
go test -short ./...                        # 单元测试（跳过集成测试）
go test -v -race ./...                      # 全量 + 竞态检测（merge 前必跑）
go test -v ./internal/adapter/handler/...  # 指定包
go test -run Integration ./...             # 仅集成测试
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# --- K8s (hub.lurus.cn 生产 = R6, ns lurus-newhub) ---
ssh root@100.122.83.20 "kubectl get pods -n lurus-newhub"                # R6 Tailscale;备用 ssh -p 12222 root@43.226.45.87
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deploy/lurus-newhub --tail=100"
# 部署 = merge main → docker-image-main.yml 出 :main 镜像并 auto-pin r6-stage
# manifest（[skip ci] commit）→ ArgoCD Application（deploy/k8s/argocd/,
# automated+selfHeal, prune off）自动收敛。禁手工 kubectl set image——selfHeal
# 会把它回滚;应急路径与回滚见 doc/runbook/staging-deploy.md。
# 后备（ArgoCD 不可用时）: SKIP_SECRETS=1 bash scripts/deploy-stage.sh
# migration 与 leadership lease 解耦(57e22c8a):每个 master-capable 副本启动都跑,
# 两把 advisory lock 串行化。带 migration 的部署后核 schema_migrations;
# 漂移信号 = /metrics 的 lurus_gateway_schema_migrations_pending 与
# /api/health 的 checks.schema_migrations。
```

## K8s Deployment Facts

> 全表 2026-08-24 对 **live deployment 逐项重核**（旧表整张是 2026-04 退役的
> `lurus-api`/ns `lurus-system` 的配置——nodeSelector、Redis 地址、Meilisearch、
> ALLOWED_ORIGINS、Secret 名、出站代理**全部是错的**)。真源 =
> `deploy/k8s/r6-stage/deployment.yaml`。

| Key | Value |
|-----|-------|
| Replicas | `3`（leader election 抽屉演练用;仅 leader 跑 master-only 后台任务) |
| nodeSelector | **无**(单节点集群 `cloud-ubuntu-5-32c32g`) |
| Resources | req: 256Mi/100m  lim: 1Gi/500m |
| Security | runAsUser:65534, readOnlyRootFilesystem, drop ALL caps |
| Volumes | `data: emptyDir`, `tmp: emptyDir` (no persistent disk) |
| Redis | `redis://redis.lurus-system.svc.cluster.local:6379/2`(**DB 2**,不是 0) |
| Meilisearch | `MEILISEARCH_ENABLED=false`(集群内无 meilisearch 实例) |
| 出站代理 | **无,且不要加**——上游供应商直连 :443。被墙供应商走 per-channel proxy;理由与实测见 `deploy/k8s/r6-stage/netpol-nats-egress.yaml` 头注释 |
| Egress 管控 | NetworkPolicy `newhub-nats-egress`：仅放行 NATS/DNS/PG/lurus-system/lurus-platform + 公网 80/443（RFC1918 全 except) |
| ALLOWED_ORIGINS | `https://hub.lurus.cn,https://identity.lurus.cn`(2026-08-30 切换时**移除** test-newhub——它已是 UAT 的 origin,留着=授权弱加固实例对生产发凭证跨域调用) |
| 边缘 | **无 k8s Ingress**;宿主 nginx vhost:`hub.lurus.cn`→`proxy_pass http://127.0.0.1:30850`(生产),`test-newhub.lurus.cn`→`:30851`(UAT,2026-08-30 切换),源码在 `deploy/r6-host-nginx/`。均设 `X-Forwarded-For`/`X-Real-IP` ⇒ **pod 侧 `RemoteAddr` 恒为私网**,任何「靠 RemoteAddr 判内外网」的守卫在此拓扑下恒真 |
| SSO/Cookie | `OIDC_REDIRECT_URI=https://hub.lurus.cn/api/v2/oauth/callback`、`SESSION_COOKIE_DOMAIN=""`(host-only;2026-08-30 切换,旧值 test-newhub 曾让 hub 域浏览器拒收 cookie ⇒ hub 域 SSO 一直是坏的)、`OIDC_POST_LOGOUT_REDIRECT_URI=https://hub.lurus.cn/`。IdP 侧 redirect 由 platform `config/apps.yaml` newhub 条目 domain 派生(单 URI,client_id 稳定) |
| `/metrics` | 双层封堵(2026-08-25):nginx `location = /metrics { return 404; }` + 应用层 `metricsAuthMiddleware`(无转发头的直连放行、带转发头则要 `METRICS_AUTH_TOKEN`)。唯一合法抓取者 = 宿主 netdata go.d job `newhub` → `http://localhost:30850/metrics`(直连,不经 nginx) |
| SYNC_FREQUENCY | `60`(秒;渠道缓存同步) |
| Secret | `lurus-newhub-secrets`,注入 7 个 key：SESSION_SECRET, SQL_DSN, OIDC_CLIENT_ID, IDENTITY_SESSION_SECRET, IDENTITY_SERVICE_INTERNAL_KEY, LURUS_WHITELABEL_MASTER_SECRET, TAVILY_API_KEY(optional) |

## UAT Instance (2026-08-30 起)

隔离 UAT 实例 `ns lurus-newhub-uat` / **NodePort 30851** / 域名 **`https://test-newhub.lurus.cn`**(2026-08-30 nginx 切换,原指生产 30850):独立 PG 库 `newhub_uat`、Redis DB 3、与生产**同 digest**(auto-pin 双写两个 manifest)。有意差异:OIDC/billing-unified/NATS 关、`E2E_BRIDGE_TOKEN` 开(bridge 登录)、web 限流 600、注册关(`options.RegisterEnabled=false`,代码默认开)。session cookie host-only + **Secure**(⇒ 隧道 `http://localhost:30851` 的浏览器流会丢 cookie,浏览器/e2e 走域名;API 级隧道照旧)。真源 = `deploy/k8s/r6-uat/`(README 有对照表)。**e2e**:`cd web && E2E_BRIDGE_TOKEN=$(ssh … kubectl -n lurus-newhub-uat get secret …) bun run test:e2e`(E2E_BASE_URL 默认即 test-newhub 域;2026-08-30 域名双跑 33 passed/1 legit skip×2,状态幂等)。**CI 夜跑**:web-ci.yml schedule 19:00 UTC(03:00 北京)跑 e2e job,repo secret `E2E_BRIDGE_TOKEN`(R6 侧生成,轮换后要 `gh secret set` 同步)。

## Environment Variables

env 详表（Required / platform Integration / OIDC / Meilisearch / Runtime Tuning / Observability / Proxy / OAuth）见 **`.env.example`** 与 **`doc/claude-md-archive-2026-06-10.md`**。
⚠️ Observability: 监控栈已切 **Netdata 自托管**（lurus/CLAUDE.md HARD RULE）；旧 OTLP→jaeger-collector...:4318 collector 已停。服务继续暴露 Prometheus-format `/metrics` 由 go.d `prometheus` collector 主动抓，**禁为换栈改业务代码**。

## Internal API Scopes

路径前缀 `/internal`，需 **`X-API-Key: lurus_ik_…`** 头 + scope 匹配（`repo.ScopeXxx`）。
⚠️ 2026-08-25 实测订正：此处原写 `Authorization: Bearer <key>`，是错的——`middleware.InternalApiAuth`
（`internal_api_auth.go:14`）只读 `X-API-Key`，用 Bearer 会拿到 401 `API key required`。
`/internal/admin/*`（backfill-token-accounts / convergence-stats / rotate-due-tokens）要 `ScopeAdmin`；线上唯一那把
key `platform-core` 只有 `balance:write / user:delete / provisioning`，**没有 admin**，调用前先确认 scope。

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

- **DB 自动迁移**: 启动时 GORM 自动建表（Go 结构体驱动）+ embedded SQL migration runner（`internal/pkg/migration`，**每个 master 副本**上自动跑并由 advisory lock 串行化，`MIGRATIONS_AUTO_RUN=false` 可关）。**Baseline 契约**: `migrations/` 001–020 只记账永不执行（001–004 是 MySQL 方言）；021 起必须 PG-only + 幂等，ID 先在 root `doc/coord/migration-ledger.md` 预留
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
