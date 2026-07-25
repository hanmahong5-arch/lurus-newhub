# Lurus Newhub (2b-svc-newhub)

AI 数据处理枢纽(P0)。New API 开源基座 + 数据处理管道 + 定制中转 + 企业计费;多租户 Hub 层(newapi 之上 + Platform 计费)。lurus-newapi = 稳定开源中转基座(定期同步上游),Hub 在其上加定制逻辑。
🔎 **K8s 部署事实 / internal scopes / 目录全表 / BMAD / env 详表 → Read `doc/claude-detail.md`**;更早全表 `doc/claude-md-archive-2026-06-10.md`;契约真源 `lurus/doc/coord/contracts.md`。

- **Module** `github.com/LurusTech/lurus-hub` · **Domain** `hub.lurus.cn`(stage `test-newhub.lurus.cn`)
- **ns/Port** `lurus-system` / pod:3000, svc:8850 · **Image** `ghcr.io/LurusTech/lurus-api:main`
- **Stack** Go 1.25.1+Gin+GORM · React18+Vite+Semi UI(`web/`,**Bun**)· PG(`lurus_api` schema)· Redis DB0 · Meilisearch(可选)· 30+ LLM providers(`internal/adapter/provider/`)
- **Auth** OIDC(issuer/clientId deploy-time owner-gated)· Passkey · session cookie/Redis

## Key Runtime Notes
- **DB 迁移**:GORM 自动建表 + embedded runner(`internal/pkg/migration`,boot lease winner 自动跑)。**Baseline 契约**:`migrations/` 001–020 只记账**永不执行**(001–004 是 MySQL 方言);**021 起必须 PG-only + 幂等**,ID 先在根 `doc/coord/migration-ledger.md` 预留。
- **PG-only**:`SQL_DSN` 非 `postgres://` 则 boot fast-fail;MySQL/SQLite dev fallback 已删(SQLite 仅 hermetic 单测 tier)。
- Redis 在则自动启用渠道内存缓存(`SYNC_FREQUENCY` 控周期);后台任务走 `lifecycle.Manager`+`TickerTask`。
- Proto `github.com/LurusTech/lurus-proto-go/identity/v1`;go.mod `replace => ../shared/lurus-proto-go`。
- 监控 Netdata 主动抓 `/metrics`,**禁为换栈改业务代码**。

## Commands
```bash
go run ./cmd/server                         # :3000(先 cp .env.example .env 填 SQL_DSN/REDIS_CONN_STRING/SESSION_SECRET)
cd web && bun install && bun run dev        # :5173 → 代理 3000
cd web && bun run typecheck && bun run lint && bun run build
CGO_ENABLED=0 go build -ldflags "-s -w -X 'github.com/LurusTech/lurus-hub/internal/pkg/common.Version=$(cat VERSION)'" -o lurus-api ./cmd/server
go test -short ./...        # 单测   |   go test -v -race ./...   # merge 前必跑
```

**Story 文档规则(Epic 6+)**:实现前建 story 文档 → 过 `dev-story/checklist.md` → 含验证证据才可标 done,违反 = 工作无效。
