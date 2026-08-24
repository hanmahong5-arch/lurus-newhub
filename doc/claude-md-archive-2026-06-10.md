# newhub CLAUDE.md Archive (2026-06-10)

> 来源: 本文件内容从 `2b-svc-newhub/CLAUDE.md` 瘦身时移出(15881B→~8KB)。
> 服务级 CLAUDE.md 应为「简介 + 命令」,不重复继承根级,也不内联全量 env 详表。
> env 变量真源 = `.env.example`;路由/契约真源 = `doc/` + `lurus/doc/coord/contracts.md`。
> 此处保留为历史参考,可能滞后,以代码/`.env.example` 为准。

## Environment Variables (full)

### Required

| Variable | Description |
|----------|-------------|
| `SQL_DSN` | PostgreSQL: `postgresql://user:pass@host/db`；MySQL: `user:pass@tcp(host:3306)/db` |
| `SESSION_SECRET` | Session 签名密钥（多节点必须一致） |
| `REDIS_CONN_STRING` | `redis://redis:6379`，缺失则退化为 cookie session |

### lurus-platform Integration

| Variable | Default | Description |
|----------|---------|-------------|
| `IDENTITY_SERVICE_URL` | `http://platform-core.lurus-platform.svc.cluster.local:18104` | HTTP 地址 |
| `IDENTITY_GRPC_ADDR` | `platform-core.lurus-platform.svc.cluster.local:18105` | gRPC 地址（自动 HTTP fallback） |
| `IDENTITY_SERVICE_INTERNAL_KEY` | — | platform `/internal/v1/*` bearer token |
| `IDENTITY_SESSION_SECRET` | — | 与 lurus-platform 共享的 session token 验签密钥 |
| `IDENTITY_AUTH_REDIRECT` | `false` | `true` → register/login/topup 重定向到 identity |
| `IDENTITY_PUBLIC_URL` | `https://identity.lurus.cn` | 用于 redirect URL 构造 |

### Zitadel OIDC (v2 API)

| Variable | Default | Description |
|----------|---------|-------------|
| `ZITADEL_ENABLED` | `false` | 启用 Zitadel OIDC |
| `ZITADEL_ISSUER` | — | `https://auth.lurus.cn` |
| `ZITADEL_JWKS_URI` | — | `https://auth.lurus.cn/oauth/v2/keys` |
| `ZITADEL_CLIENT_ID` | — | Zitadel app client ID |
| `ZITADEL_REDIRECT_URI` | — | 生产: `https://api.lurus.cn/api/v2/oauth/callback` |
| `ZITADEL_POST_LOGOUT_REDIRECT_URI` | — | 登出后跳转 URL |
| `ZITADEL_ALLOWED_REDIRECT_DOMAINS` | — | `lurus.cn,api.lurus.cn` |
| `ZITADEL_ENABLE_PKCE` | `false` | 启用 PKCE |
| `ZITADEL_AUTO_CREATE_USER` | `false` | OIDC 登录自动建用户 |
| `ZITADEL_AUTO_CREATE_TENANT` | `false` | OIDC 登录自动建租户 |

### Meilisearch (Optional)

| Variable | Default | Description |
|----------|---------|-------------|
| `MEILISEARCH_ENABLED` | `false` | 启用 Meilisearch |
| `MEILISEARCH_HOST` | — | `http://meilisearch:7700` |
| `MEILISEARCH_API_KEY` | — | Master key |
| `MEILISEARCH_SYNC_ENABLED` | `false` | 启用日志同步 |
| `MEILISEARCH_SYNC_WORKERS` | `32` | 同步并发数 |
| `MEILISEARCH_SYNC_BATCH_SIZE` | `1000` | 批次大小 |
| `MEILISEARCH_WORKER_COUNT` | `2` | 生产用 worker 数 |

### Runtime Tuning

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `3000` | HTTP 监听端口 |
| `GIN_MODE` | `debug` | `release` 关闭 debug 输出 |
| `DEBUG` | `false` | 启用 debug 日志 |
| `NODE_TYPE` | `master` | `slave` 禁用 master-only 任务 |
| `SYNC_FREQUENCY` | `60` | 缓存同步周期（秒） |
| `RELAY_TIMEOUT` | `0` | Relay 超时（秒，0=不限） |
| `RELAY_MAX_IDLE_CONNS` | `500` | HTTP 连接池最大空闲连接 |
| `BATCH_UPDATE_ENABLED` | `false` | 启用批量数据库写入 |
| `BATCH_UPDATE_INTERVAL` | `5` | 批量写入间隔（秒） |
| `DAILY_QUOTA_ENABLED` | `true` | `false` 禁用每日配额重置 |
| `CHANNEL_UPDATE_FREQUENCY` | — | 渠道自动更新频率（分钟） |
| `MODEL_SYNC_FREQUENCY` | — | 模型自动同步频率（分钟） |
| `MEMORY_CACHE_ENABLED` | `false` | 启用内存缓存（Redis 存在时自动开启） |
| `STREAMING_TIMEOUT` | `300` | 流式请求无响应超时（秒） |
| `MAX_REQUEST_BODY_MB` | `64` | 请求体最大大小（MB） |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `30s` | 优雅停机等待时间 |
| `ALLOWED_ORIGINS` | 见 config.go | CORS 允许域名（逗号分隔） |
| `FRONTEND_BASE_URL` | — | slave 节点将前端路由重定向到此 URL |
| `MINIO_RELEASES_BUCKET` | `lurus-releases` | Release 文件的 MinIO bucket |

### Observability (Optional)

> ⚠️ 2026-06-05 治理修正: 监控栈已全量切换为 **Netdata 自托管 Agent**(见 lurus/CLAUDE.md 可观测性 HARD RULE)。
> 旧 OTLP→jaeger-collector...:4318 collector **已停**;服务侧继续暴露 Prometheus-format `/metrics` 由 go.d `prometheus` collector 主动抓,**禁为换栈改业务代码**。
> 下表 OTEL_* 变量保留为历史参考,当前生产不再指向 jaeger。

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_TRACING_ENABLED` | `false` | 启用 OpenTelemetry traces |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | (旧) `jaeger-collector.observability.svc:4318` — collector 已停 |
| `OTEL_EXPORTER_OTLP_INSECURE` | — | `true` |
| `OTEL_TRACE_SAMPLE_RATE` | `0.1` | 采样率（0.0~1.0） |
| `OTEL_ENVIRONMENT` | — | `production` |
| `ENABLE_PPROF` | `false` | 启用 pprof（port 8005） |
| `LOG_FORMAT` | — | `json` 启用结构化日志 |
| `LOG_LEVEL` | — | 日志级别 |

### Proxy (For External LLM APIs) — ⚠️ 2026-08-24 作废，勿照抄

下表是 2026-06 快照，**现在一条都不成立**：live deployment 根本没有任何 `*_PROXY` env，
`10.42.1.1:10808` **无进程监听**（该节点 cni0 是 `10.42.0.1`，且 `:10808` 上什么都没有），
即便有也到不了——NetworkPolicy `newhub-nats-egress` 把 pod 的 RFC1918 出站全 except 掉了。

现行 egress 模型（直连 :443 + 被墙供应商走 per-channel proxy）与实测证据写在
`deploy/k8s/r6-stage/netpol-nats-egress.yaml` 头注释，**加代理前先读那里**。

| Variable | Value（2026-06 快照，已作废） | Description |
|----------|--------------------|-------------|
| `HTTP_PROXY` / `http_proxy` | ~~`http://10.42.1.1:10808`~~ | 出站代理（访问 OpenAI/Gemini 等） |
| `HTTPS_PROXY` / `https_proxy` | ~~`http://10.42.1.1:10808`~~ | — |
| `NO_PROXY` / `no_proxy` | ~~`localhost,127.0.0.1,10.0.0.0/8,*.svc,*.lurus.cn…`~~ | 内网绕过代理 |

### OAuth Providers (Optional)

`GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `ALIPAY_PRIVATE_KEY`, `ALIPAY_PUBLIC_KEY`,
`WECHAT_SERVER_ADDRESS`, `WECHAT_SERVER_TOKEN`, `TELEGRAM_BOT_TOKEN`,
`UMAMI_WEBSITE_ID`, `GOOGLE_ANALYTICS_ID`

## Route Groups

| Group | Auth | Description |
|-------|------|-------------|
| `GET /api/status` | public | Healthcheck → `{"success": true}` |
| `/api/*` | session (v1) | 用户/管理/渠道/Token 等 v1 API |
| `/api/v2/:tenant_slug/*` | Zitadel JWT | 多租户 v2 API (渠道/Token/日志/配置/兑换码) |
| `/api/v2/oauth/*` | public | Zitadel OAuth callback/logout/refresh |
| `/api/v2/switch/*` | public | lurus-switch 版本查询 + 预设库 |
| `/api/v2/user/identity-overview` | Zitadel JWT | VIP/钱包/订阅信息（来自 platform） |
| `/api/v2/admin/*` | v1 session + RootAuth | 平台管理员：租户管理/用户映射/统计 |
| `/v1/*` | Token auth | Relay: chat/completions, messages(Claude), responses, images, audio, embeddings, rerank, realtime(WS) |
| `/v1beta/models/*` | Token auth | Gemini relay |
| `/mj/*`, `/:mode/mj/*` | Token auth | Midjourney relay |
| `/suno/*` | Token auth | Suno task relay |
| `/pg/chat/completions` | User session | Playground |
| `/internal/*` | API Key + Scope | 服务内通信（见 Internal API Scopes） |
| `GET /metrics` | public | Prometheus scrape |

## lurus-platform gRPC Integration

`internal/pkg/common/identity_grpc_client.go` — singleton gRPC client，连接失败自动 fallback 到 HTTP。

| Function | gRPC Method | Description |
|----------|-------------|-------------|
| `GetAccountByZitadelSubGRPC` | `GetAccountByZitadelSub` | 通过 Zitadel sub 查账户 |
| `UpsertAccountGRPC` | `UpsertAccount` | 创建或更新账户（OIDC 首次登录） |
| `GetEntitlementsGRPC` | `GetEntitlements` | 查权益（产品功能开关） |
| `GetAccountOverviewGRPC` | `GetAccountOverview` | 聚合：账户 + VIP + 钱包 + 订阅 |
| `ReportLLMUsageGRPC` | `ReportUsage` | 上报 LLM 用量 (amountCNY) |
| `DebitWalletGRPC` | `WalletDebit` | 钱包扣款（消费 LLM 时） |
| `CreditWalletGRPC` | `WalletCredit` | 钱包充值 |

gRPC auth: Bearer token in metadata `authorization` header (同 `IDENTITY_SERVICE_INTERNAL_KEY`)。

## Relay Formats

`internal/pkg/types/relay_format.go` 定义，`handler.Relay(c, types.RelayFormatXxx)` 分发。

| RelayFormat | Endpoint | Notes |
|-------------|----------|-------|
| `RelayFormatOpenAI` | `/v1/chat/completions`, `/v1/completions` | 主流 chat |
| `RelayFormatClaude` | `/v1/messages` | Anthropic 原生格式 |
| `RelayFormatGemini` | `/v1beta/models/*`, `/v1/models/*path` | Gemini 原生格式 |
| `RelayFormatOpenAIResponses` | `/v1/responses` | OpenAI Responses API |
| `RelayFormatOpenAIImage` | `/v1/images/generations`, `/v1/images/edits`, `/v1/edits` | 图像生成 |
| `RelayFormatEmbedding` | `/v1/embeddings`, `/v1/engines/:model/embeddings` | Embeddings |
| `RelayFormatOpenAIAudio` | `/v1/audio/*` | TTS/ASR |
| `RelayFormatRerank` | `/v1/rerank` | Rerank |
| `RelayFormatOpenAIRealtime` | `GET /v1/realtime` (WebSocket) | Realtime API |

Midjourney/Suno 通过独立 handler 处理（`handler.RelayMidjourney`, `handler.RelayTask`）。
