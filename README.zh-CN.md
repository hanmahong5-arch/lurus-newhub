中文 | [English](./README.md)

# Lurus Hub

多租户大模型网关：统一一个 API 接入 30+ 家模型供应商，具备按租户隔离、用量分析、以及可选的计费对接能力。

## 这是什么

Lurus Hub（Go module 名 `lurus-hub`，仓库名 `lurus-newhub`）是在 [New API](https://github.com/QuantumNous/new-api) 中转基座上定制的衍生项目，New API 本身又衍生自 [One API](https://github.com/songquanpeng/one-api)。在中转能力之上，本仓加了一层数据处理逻辑：按渠道打分、用量聚合、多租户 OIDC 认证，以及一个可选的 gRPC 钩子，用于把用量上报给外部计费服务。

它是 Lurus 自家产品线的生产大模型网关（`hub.lurus.cn`），因此中转链路、多租户路由、V1/V2 REST API 都在持续实跑。但部分能力默认关闭、按部署环境自行开启：Meilisearch 日志搜索（`MEILISEARCH_ENABLED=false`）、OpenTelemetry 链路追踪（`OTEL_TRACING_ENABLED=false`）、OIDC 登录（`OIDC_ENABLED=false`）、外部计费对接（`BILLING_UNIFIED_ENABLED=false`）。这些是"代码已具备、默认关闭"，不代表在你的部署环境里已经端到端交付——开启后请自行验证。

## 核心能力

- **统一多供应商中转** — 在 20+ 家模型供应商前提供 OpenAI 兼容接口，并在三种供应商 API 格式间自动转换（`internal/adapter/provider/`、`internal/adapter/handler/router/relay-router.go`）。
- **渠道打分与用量聚合** — 独立于中转链路本身的"数据枢纽"层：加权渠道选择、健康度打分、滚动用量聚合（`internal/app/hub/channel_scorer.go`、`internal/app/hub/usage_aggregator.go`）。
- **多租户 REST API** — 按 `tenant_slug` 划分的 V2 API，带角色权限（admin/user/billing_manager），并保留一套单租户兼容的 V1 接口供既有集成使用（`internal/adapter/handler/router/api-v2-router.go`、`api-router.go`）。
- **厂商中立的 OIDC 认证** — 通过标准发现文档（`.well-known/openid-configuration`）对接任意标准 OIDC 身份提供方；默认关闭，配置见 `.env.example`（`OIDC_*`）。
- **可选的计费对接钩子** — 通过 gRPC/HTTP 把用量上报给配套的账户计费服务；未配置时服务端用自带的 session 认证独立运行（`internal/pkg/common/identity_grpc_client.go`、`usage_report.go`）。
- **Prometheus 指标** — `/metrics` 输出 Prometheus 文本格式，请求带反代转发头时会走鉴权检查（`internal/adapter/handler/router/main.go:37,85`；指标定义见 `internal/pkg/metrics/metrics.go`）。

## 快速开始

```bash
# --- 后端 ---
cp .env.example .env                # 填写 SQL_DSN 与 SESSION_SECRET(均必填,无默认值)
go run ./cmd/server                  # 监听 :3000 (PORT)

# --- 前端控制台 (Bun; web/ 有独立 package.json) ---
cd web && bun install && bun run dev # :5173, API 请求代理到 :3000

# --- 测试 ---
go test -short ./...                 # 仅单元测试,跳过集成测试(testing.Short())
go test -short -race -count=1 -timeout=15m ./...  # CI 强制合并门(.github/workflows/go-ci.yml)
cd web && bun run test && bun run lint && bun run eslint

# --- Docker Compose (自带 server + Postgres + Redis) ---
docker-compose up -d                 # http://localhost:3000

# --- 生产构建 ---
CGO_ENABLED=0 go build -ldflags "-s -w -X 'github.com/LurusTech/lurus-hub/internal/pkg/common.Version=$(cat VERSION)'" -o lurus-api ./cmd/server
```

注意事项：
- `SQL_DSN` 必须是 `postgres://` 或 `postgresql://`——其他协议(或未设置)会直接拒绝启动;MySQL 与 SQLite 的开发降级路径已被移除。`REDIS_CONN_STRING` 推荐但非必填(未设置时降级为仅 cookie 会话,仅限开发环境)。
- `go.mod` 通过本地 `replace ... => ../shared/lurus-proto-go` 指令锁定 `github.com/LurusTech/lurus-proto-go`。在本 monorepo 之外构建,需要这个同级模块实际存在(参见两阶段 `Dockerfile` 里 CI 是如何注入它的),或移除该 `replace` 行。
- 禁止直接跑单个 `_test.go` 文件——表驱动测试依赖包级共享 fixture,请用 `go test ./<package>/...` 或 `go test ./...`。

## 架构

```
cmd/server/main.go        # 入口
internal/
├── domain/entity/        # GORM 模型:channel、token、tenant、user、log、pricing、ability、credit pool…
├── app/                  # 业务逻辑
│   ├── relay/             # 请求分发,对接 30+ 供应商适配器
│   ├── hub/                # ChannelScorer + UsageAggregator("数据枢纽"核心)
│   └── governance/         # 审计留痕
├── adapter/
│   ├── handler/           # HTTP 控制器 + router/ (v1、v2、relay、internal、web)
│   ├── middleware/         # 鉴权、CORS、限流、分发
│   ├── repo/                # GORM repository 层
│   └── provider/            # 各厂商适配器 (openai/、claude/、gemini/、aws/、baidu/ 等)
├── lifecycle/             # leader election、优雅关闭、密钥轮换
└── pkg/                   # config、logger、metrics、tracing、search、migration、nats、resilience
web/                       # React 18 + Vite + Semi UI 控制台 (Bun;6 种语言,见 web/src/i18n/locales/)
migrations/                # PostgreSQL SQL migration (001-020 为历史记账用基线,部分仅 MySQL 方言;021 起才是当前 PG-only、幂等的真实序列)
deploy/k8s/                # Kubernetes 清单 (staging/UAT overlay)
```

## 配置

完整清单见 [`.env.example`](./.env.example)。以下为部分关键变量：

| 变量 | 是否必填 | 默认值 | 说明 |
|---|---|---|---|
| `SQL_DSN` | 是 | — | PostgreSQL 连接串;非 Postgres DSN 直接拒绝启动 |
| `SESSION_SECRET` | 是 | — | Session 签名密钥;多节点部署必须一致 |
| `REDIS_CONN_STRING` | 推荐 | `redis://redis:6379` | Session 存储 + 渠道缓存;未设置时降级为仅 cookie 会话 |
| `PORT` | 否 | `3000` | HTTP 监听端口 |
| `GIN_MODE` | 否 | `debug` | `debug` 或 `release` |
| `MIGRATIONS_AUTO_RUN` | 否 | `true` | 启动时是否跑内置 SQL migration runner |
| `OIDC_ENABLED` | 否 | `false` | 启用 OIDC 登录;为 true 时 `OIDC_ISSUER`/`OIDC_JWKS_URI`/`OIDC_CLIENT_ID` 变为必填 |
| `MEILISEARCH_ENABLED` | 否 | `false` | 日志全文搜索 |
| `IDENTITY_SERVICE_URL` / `IDENTITY_GRPC_ADDR` | 否 | — | 可选的配套账户计费服务(用量上报、钱包扣费) |
| `BILLING_UNIFIED_ENABLED` | 否 | `false` | 切换为预授权/冻结/结算的计费流程,而非事后扣费 |
| `OTEL_TRACING_ENABLED` | 否 | `false` | 是否通过 OTLP 导出链路追踪 |
| `METRICS_AUTH_TOKEN` | 否 | (空) | 请求带反代转发头时读取 `/metrics` 需要此令牌;无转发头的直连请求恒放行 |

## 接口概览

| 接口面 | 路径前缀 | 说明 |
|---|---|---|
| V1(历史遗留,单租户兼容) | `/api/{user,token,channel,redemption,log,data,wallet}/*` | `router/api-router.go` |
| V2(多租户) | `/api/v2/:tenant_slug/{tokens,projects,channels,logs,redemptions,sessions,models,pricing,billing,chat}/*`,`/api/v2/admin/{tenants,mappings,internal-keys,users,governance}/*` | 角色权限(admin/user/billing_manager);`router/api-v2-router.go` |
| Relay(OpenAI 兼容 + 原生供应商格式) | `POST /v1/chat/completions`、`/v1/messages`、`/v1/embeddings`、`/v1/images/generations`、`/v1/audio/*`、`/v1/rerank`;`GET /v1/models`、`/v1beta/models` | `router/relay-router.go` |
| Internal(服务间调用) | `/internal/{user,token,quota,balance,currency,log,models,admin}/*` | 鉴权头是 `X-API-Key` + scope 匹配,**不是** `Authorization: Bearer`;`router/internal-api-router.go` |

完整 OpenAPI 规范：[`docs/openapi/api-v2.yaml`](./docs/openapi/api-v2.yaml)。

## 开发约定

- 测试文件命名 `*_test.go` / `*_integration_test.go` / `*_benchmark_test.go`;测试函数命名 `Test<Subject>_<Method>_<Behavior>`,优先表驱动写法(详见 [`TESTING.md`](./TESTING.md))。
- CI 按分层强制覆盖率下限，随覆盖率提升逐步上调（ratchet）：`internal/app/` ≥ 86%、`internal/adapter/repo/` ≥ 77%、`internal/adapter/handler/` ≥ 64%（`.github/workflows/go-ci.yml`）。
- `web/` 是纯 JS,不是 TypeScript——没有 `typecheck` 脚本;前端真实的检查闸是 `bun run lint`(prettier)与 `bun run eslint`。
- 数据库 schema 变更以 `migrations/` 下新文件的形式提交,`021_` 起为 PostgreSQL-only 且幂等。
- 部署走 GitOps:合并进 `main` 会构建并发布镜像,由另一个独立的协调器完成集群收敛——本仓自身不直接驱动 `kubectl`(详见 [`DEPLOY.md`](./DEPLOY.md))。

## 相关项目

在 Lurus 平台内部,本服务暴露了一组专用路由 `/api/v2/switch/*`(`router/api-v2-router.go`),供 Switch 桌面客户端(仓库 `lurus-switch`)消费,用于激活码兑换、渠道/令牌管理。可选的计费钩子(见上文 `IDENTITY_GRPC_ADDR`)对接的是配套的账户计费核心服务;两处集成均默认关闭、需显式配置才生效,因此本仓可以完全独立运行。

## 许可证与上游致谢

许可条款见 [`LICENSE`](./LICENSE)：**默认 AGPLv3**,若需移除上游品牌标识、或规避 AGPLv3 的网络服务源码公开义务等场景（完整场景清单见该文件），则**必须**获取商业许可证。这套授权模式,包括开源层级下额外的品牌保留限制,均继承自上游项目。

本项目是以下项目的定制衍生：
- [New API](https://github.com/QuantumNous/new-api)（AGPLv3,双重授权）——本项目持续跟踪、cherry-pick 的直接上游基座。
- [One API](https://github.com/songquanpeng/one-api)（MIT）——New API 本身衍生自的更早期项目。

第三方组件许可证摘要（Go 模块与 `web/` 前端依赖）见 [`NOTICE`](./NOTICE)。
