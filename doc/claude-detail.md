# Newhub 细节参考(从 CLAUDE.md 下沉,按需 Read)

改 K8s 部署 / internal API / BMAD 流程前 Read 本文件。契约真源 = `lurus/doc/coord/contracts.md`;
更早的 Route Groups / gRPC Integration / Relay Formats 全表 = `doc/claude-md-archive-2026-06-10.md`。

## Core Capabilities

| Capability | Description |
|------------|-------------|
| **Data Processing** | Real-time LLM usage analytics, cost optimization, model performance monitoring |
| **Custom Relay** | Per-product routing rules, per-tenant model pools, quota enforcement |
| **Billing Integration** | Usage metering → Platform billing (gRPC ReportUsage + WalletDebit) |
| **Multi-tenant** | Channel/Token/quota/log isolation per tenant |
| **Upstream Sync** | Cherry-pick New API upstream fixes monthly; security patches immediately |

## K8s Deployment Facts

| Key | Value |
|-----|-------|
| nodeSelector | `lurus.cn/vpn: "true"` |
| Resources | req: 256Mi/100m  lim: 1Gi/500m |
| Security | runAsUser:65534, readOnlyRootFilesystem, drop ALL caps |
| Volumes | `data: emptyDir`, `tmp: emptyDir` (no persistent disk) |
| Redis | `redis://redis:6379` (in-cluster) |
| Meilisearch | `http://meilisearch:7700` (in-cluster) |
| Outbound proxy | `http://10.42.1.1:10808` (Gemini/OpenAI/外网 LLM) |
| NO_PROXY | `*.svc,*.svc.cluster.local,*.lurus.cn,10.0.0.0/8,…` |
| ALLOWED_ORIGINS | `https://www.lurus.cn,https://lucrum.lurus.cn` |
| MODEL_SYNC_FREQUENCY | `60` (分钟) |
| Secret | `lurus-api-secrets` (SESSION_SECRET, SQL_DSN, OIDC_CLIENT_ID, IDENTITY_SESSION_SECRET, IDENTITY_SERVICE_INTERNAL_KEY, ALIPAY_*) |

```bash
ssh root@100.98.57.55 "kubectl get pods -n lurus-system"
ssh root@100.98.57.55 "kubectl rollout restart deployment/lurus-api -n lurus-system"
ssh root@100.98.57.55 "kubectl logs -n lurus-system -l app=lurus-api --tail=100"
```

## Internal API Scopes

路径前缀 `/internal`,需 `Authorization: Bearer <key>` + scope 匹配(`repo.ScopeXxx`)。

| Scope | Endpoints |
|-------|-----------|
| `ScopeUserRead` | `GET /internal/user/:id`, `/user/by-email/:email`, `/user/by-phone/:phone` |
| `ScopeUserWrite` | `PUT /internal/user/:id` |
| `ScopeQuotaRead` | `GET /internal/quota/user/:id` |
| `ScopeQuotaWrite` | `POST /internal/quota/adjust` |
| `ScopeBalanceRead` | `GET /internal/balance/user/:id` |
| `ScopeBalanceWrite` | `POST /internal/balance/topup` |

## 目录结构(全)

```
cmd/server/main.go           # Entry point
internal/
├── domain/entity/           # channel, user, log, token, tenant, task…
├── app/                     # relay/, passkey/, billing, quota, channel…
│   └── relay/               # Multi-modal request dispatch (30+ providers)
├── adapter/
│   ├── handler/             # HTTP controllers + router/ (v1/v2/relay/internal/web)
│   ├── middleware/          # Auth, CORS, rate-limit, distributor, stats
│   ├── repo/                # GORM repositories
│   └── provider/            # AI vendor adapters (openai, claude, gemini, aws, +18 more)
├── lifecycle/               # Background task lifecycle manager
└── pkg/                     # config, common, constant, dto, types, logger, metrics, search, setting, tracing
web/                         # React frontend (Bun)
migrations/                  # PostgreSQL SQL migrations
deploy/k8s/                  # K8s manifests + staging overlay
```

## BMAD

| Resource | Path |
|----------|------|
| PRD | `./_bmad-output/planning-artifacts/prd.md` |
| Epics | `./_bmad-output/planning-artifacts/epics.md` |
| Architecture | `./_bmad-output/planning-artifacts/architecture.md` |
| Sprint Status | `./_bmad-output/planning-artifacts/sprint-status.yaml` |
| Project Context | `./_bmad-output/planning-artifacts/project-context.md` |

**Story 文档规则(Epic 6+ 严格执行)**:实现前建 story 文档 → 通过 `dev-story/checklist.md` → 含验证证据才可标 done。违反 = 工作无效。
_BMAD artifacts last review: 2026-05-18 — governance `lurus/doc/audit/2026-05-18-bmad-output-stale.md`_

## Environment Variables

详表(Required / platform Integration / OIDC / Meilisearch / Runtime Tuning / Observability / Proxy / OAuth)见 `.env.example` 与 `doc/claude-md-archive-2026-06-10.md`。
⚠️ 监控栈已切 **Netdata 自托管**(根 CLAUDE.md HARD RULE);旧 OTLP→jaeger-collector:4318 已停。服务继续暴露 Prometheus-format `/metrics` 由 go.d `prometheus` collector 主动抓,**禁为换栈改业务代码**。
