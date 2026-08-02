---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]
inputDocuments: ['project-context.md', 'product-brief.md', 'architecture.md', 'lurus-api/doc/plan.md', 'lurus-api/doc/process.md', 'lurus-api/docs/openapi/api.json']
workflowType: 'prd'
projectType: 'brownfield'
date: '2026-02-02'
author: 'Anita (via BMAD Analysis)'
service: 'lurus-api'
---

# Product Requirements Document: Lurus API
# 产品需求文档：Lurus API

---

## 1. Executive Summary / 执行摘要

### 1.1 Product Overview / 产品概述

Lurus API is a **multi-tenant LLM API gateway** that provides a unified, OpenAI-compatible interface for accessing 30+ AI model providers. It serves as the core infrastructure of the Lurus Platform, handling authentication, billing, quota management, and request relay to upstream LLM providers.

Lurus API 是一个**多租户 LLM API 网关**，提供统一的 OpenAI 兼容接口，用于访问 30+ AI 模型供应商。它是 Lurus 平台的核心基础设施，处理认证、计费、配额管理和向上游 LLM 供应商的请求转发。

### 1.2 Current State / 当前状态

| Aspect | Status |
|--------|--------|
| Production URL | https://api.lurus.cn |
| API Version | v1 (production), v2 (multi-tenant, in progress) |
| Tech Stack | Go 1.25.1 + Gin + GORM + PostgreSQL + Redis |
| Authentication | Session-based (v1) + Zitadel OIDC JWT (v2) |
| Multi-Tenancy | Code complete, pending Zitadel configuration |
| Test Coverage | ~70% (app/ layer), ~50% (adapter/handler/ layer) |
| Deployment | K3s (lurus-system namespace) via GitOps |

### 1.3 Key Stakeholders / 关键利益相关者

| Stakeholder | Role | Interest |
|-------------|------|----------|
| API Consumers | LLM API users (developers/enterprises) | Reliable, low-latency API access |
| Team Owner (Anita) | Full-stack developer, platform operator | Operational simplicity, extensibility |
| Tenant Admins | Business operators on the platform | User/quota/billing management |
| Platform Admin | Root-level system administrator | Cross-tenant oversight, system health |

---

## 2. Success Criteria / 成功标准

### 2.1 North Star Metric / 北极星指标

**API Gateway Availability ≥ 99.5%** with p95 latency overhead < 50ms (gateway processing, excluding upstream provider latency).

### 2.2 Measurable Outcomes / 可衡量的结果

| ID | Metric | Current | Target | Measurement |
|----|--------|---------|--------|-------------|
| SC-1 | Monthly uptime | ~98% | ≥ 99.5% | Uptime monitoring |
| SC-2 | Gateway overhead p95 | ~80ms | < 50ms | Request log analysis |
| SC-3 | Supported LLM providers | 30+ | 35+ | Channel type count |
| SC-4 | Multi-tenant support | Code complete | Production | End-to-end test pass |
| SC-5 | v2 API endpoint coverage | 26 endpoints | 30+ endpoints | Route count |
| SC-6 | Test coverage (app/) | **86.2% ✅ MET** | ≥ 80% | `go test -cover` (CI coverage-gate, main run 30152927620, 2026-07-25) |
| SC-7 | Test coverage (adapter/handler/) | **52.6% ✅ MET** | ≥ 50% | `go test -cover` (same run) |
| SC-8 | Active tenants | 1 (default) | 5+ | Tenant table count |

---

## 3. Product Scope / 产品范围

### 3.1 In-Scope Features / 范围内功能

#### Feature Group 1: LLM API Relay (Core) / LLM API 转发 (核心)

**Description**: OpenAI-compatible API gateway that relays requests to 30+ LLM providers with unified interface, streaming support, and automatic retry/failover.

| Feature | Priority | Status |
|---------|----------|--------|
| OpenAI-compatible chat completions | P0 | ✅ Production |
| OpenAI-compatible embeddings | P0 | ✅ Production |
| OpenAI-compatible image generation | P0 | ✅ Production |
| OpenAI-compatible audio (TTS/STT) | P1 | ✅ Production |
| Streaming response (SSE) | P0 | ✅ Production |
| Model-to-channel routing | P0 | ✅ Production |
| Cross-group retry / failover | P0 | ✅ Production |
| Model mapping (name aliasing) | P1 | ✅ Production |
| Pass-through request mode | P1 | ✅ Production |
| Web search integration | P2 | ✅ Production |

**Supported Providers (30+)**:
OpenAI, Anthropic (Claude), Google (Gemini), AWS Bedrock, Azure OpenAI, Baidu (Wenxin), Baidu V2, Alibaba (Tongyi), Tencent (Hunyuan), Zhipu (GLM), Zhipu V4, Xunfei (Spark), DeepSeek, Moonshot (Kimi), SiliconFlow, VolcEngine (Doubao), Cohere, Mistral, Cloudflare Workers AI, Ollama, Perplexity, Vertex AI, Jina, Dify, MokaAI, xAI (Grok), Coze, Jimeng, Minimax, Replicate, OpenRouter, Xinference, Submodel.

#### Feature Group 2: Task Management / 任务管理

**Description**: Asynchronous task system for AI content generation (images, video, music).

| Feature | Priority | Status |
|---------|----------|--------|
| Midjourney image generation relay | P1 | ✅ Production |
| Suno music generation relay | P1 | ✅ Production |
| Video generation (Kling, Sora, Vidu, Hailuo, Doubao, Jimeng) | P1 | ✅ Production |
| Task status polling and webhooks | P1 | ✅ Production |
| Task quota billing | P1 | ✅ Production |

#### Feature Group 3: User & Authentication / 用户与认证

**Description**: User management with multiple authentication methods.

| Feature | Priority | Status |
|---------|----------|--------|
| Email/password registration and login | P0 | ✅ Production |
| OAuth social login (GitHub, Discord, WeChat, Telegram, LinuxDo, OIDC) | P0 | ✅ Production |
| Zitadel OIDC integration (v2) | P0 | ✅ Code complete |
| Two-Factor Authentication (TOTP) | P0 | ✅ Production |
| Passkey / WebAuthn | P1 | ✅ Production |
| SMS login | P1 | ✅ Production |
| Email verification | P0 | ✅ Production |
| Password reset | P0 | ✅ Production |
| User roles (root, admin, user) | P0 | ✅ Production |
| Invitation code system | P1 | ✅ Production |
| Turnstile captcha protection | P1 | ✅ Production |

#### Feature Group 4: Multi-Tenant SaaS / 多租户 SaaS

**Description**: Transform from single-tenant to multi-tenant platform using Zitadel as identity provider.

| Feature | Priority | Status |
|---------|----------|--------|
| Tenant model (Zitadel Organization mapping) | P0 | ✅ Code complete |
| User identity mapping (Zitadel → lurus) | P0 | ✅ Code complete |
| GORM tenant isolation plugin (auto WHERE tenant_id) | P0 | ✅ Code complete |
| Tenant configuration system (key-value store) | P1 | ✅ Code complete |
| JWT verification middleware (JWKS) | P0 | ✅ Code complete |
| PKCE for OAuth security | P0 | ✅ Code complete |
| v2 API routes (/:tenant_slug/...) | P0 | ✅ Code complete |
| Platform admin tenant management | P1 | ✅ Code complete |
| v1 API backward compatibility (default tenant) | P0 | ✅ Code complete |

#### Feature Group 5: Billing & Quota / 计费与配额

**Description**: Quota-based billing with multiple payment methods and subscription plans.

| Feature | Priority | Status |
|---------|----------|--------|
| Token-based quota system | P0 | ✅ Production |
| TopUp via Epay | P0 | ✅ Production |
| TopUp via Stripe | P0 | ✅ Production |
| TopUp via Creem | P1 | ✅ Production |
| Subscription plans (weekly/monthly/quarterly/yearly) | P1 | ✅ Production |
| Subscription auto-renewal | P1 | ✅ Production |
| Daily quota cron (subscription) | P1 | ✅ Production |
| Redemption codes (batch create, redeem) | P1 | ✅ Production |
| Affiliate system (referral rewards) | P2 | ✅ Production |
| Model ratio configuration | P0 | ✅ Production |
| Tenant-level billing isolation | P0 | ✅ Code complete |
| Webhook tenant verification (Stripe, Creem, Epay) | P0 | ✅ Code complete |

#### Feature Group 6: Channel Management / 渠道管理

**Description**: Manage LLM provider channels (API keys, endpoints, model assignments).

| Feature | Priority | Status |
|---------|----------|--------|
| Channel CRUD (admin) | P0 | ✅ Production |
| Channel auto-testing (availability check) | P1 | ✅ Production |
| Channel billing tracking | P1 | ✅ Production |
| Channel grouping and tagging | P1 | ✅ Production |
| Channel priority and weight-based routing | P1 | ✅ Production |
| Channel model list fetching | P1 | ✅ Production |
| Channel cache with periodic sync | P0 | ✅ Production |
| Multi-key mode per channel | P1 | ✅ Production |

#### Feature Group 7: Token Management / 令牌管理

**Description**: API token management with fine-grained access control.

| Feature | Priority | Status |
|---------|----------|--------|
| Token CRUD | P0 | ✅ Production |
| Token quota limits | P0 | ✅ Production |
| Token model limits (whitelist) | P1 | ✅ Production |
| Token group assignment | P1 | ✅ Production |
| Token cross-group retry | P1 | ✅ Production |
| Token IP whitelist | P2 | ✅ Production |
| Token expiration | P1 | ✅ Production |

#### Feature Group 8: Logging & Search / 日志与搜索

**Description**: API usage logging with full-text search capability.

| Feature | Priority | Status |
|---------|----------|--------|
| Request/response logging | P0 | ✅ Production |
| Usage statistics and analytics | P1 | ✅ Production |
| Meilisearch full-text search integration | P1 | ✅ Production |
| Log search with filters (model, token, user, date) | P1 | ✅ Production |
| Data export (quota data) | P2 | ✅ Production |

#### Feature Group 9: System & Operations / 系统与运维

**Description**: System configuration, monitoring, and operational features.

| Feature | Priority | Status |
|---------|----------|--------|
| System options management (root) | P0 | ✅ Production |
| Graceful shutdown with context-aware tasks | P0 | ✅ Production |
| Lifecycle management (errgroup) | P0 | ✅ Production |
| Rate limiting (global + model-level) | P0 | ✅ Production |
| CPU monitoring and pprof | P2 | ✅ Production |
| Structured logging (slog) | P1 | ✅ Production |
| Centralized configuration (env vars) | P1 | ✅ Production |
| SafeGo utilities (panic recovery) | P1 | ✅ Production |
| Notify limit (alert on high usage) | P2 | ✅ Production |

#### Feature Group 10: Frontend (React SPA) / 前端 (React SPA)

**Description**: Admin dashboard and user portal (embedded in Go binary via `web/dist`).

| Feature | Priority | Status |
|---------|----------|--------|
| User dashboard | P0 | ✅ Production |
| Admin console (users, channels, tokens, logs) | P0 | ✅ Production |
| Billing and subscription management | P1 | ✅ Production |
| Markdown renderer | P1 | ✅ Production |
| Pricing page | P1 | ✅ Production |

### 3.2 Out of Scope / 范围外

| Item | Reason |
|------|--------|
| Direct LLM model hosting | Gateway-only; upstream providers host models |
| Real-money automated trading | Separate service (lurus-lucrum) |
| Email functionality | Separate service (lurus-webmail) |
| Mobile native app | Web-responsive only |
| GraphQL API | REST/OpenAI-compatible only |
| Prometheus metrics endpoint | Planned for observability initiative (see doc/decisions/observability.md) |

### 3.3 Assumptions & Dependencies / 假设与依赖

| ID | Assumption/Dependency | Risk if Invalid |
|----|----------------------|-----------------|
| A-1 | Zitadel instance available at auth.lurus.cn | Multi-tenant features blocked |
| A-2 | PostgreSQL (CNPG) on cloud-ubuntu-2 | Data layer unavailable |
| A-3 | Redis on office-debian-2 via Tailscale | Caching and rate limiting degraded |
| A-4 | Upstream LLM providers maintain API compatibility | Adaptor updates needed |
| A-5 | Meilisearch in lurus-system namespace | Search functionality unavailable |
| A-6 | GHCR accessible for container images | Deployment pipeline broken |

---

## 4. User Journeys / 用户旅程

### 4.1 Journey: API Consumer Uses LLM via Gateway / API 消费者通过网关使用 LLM

**Persona**: Developer integrating LLM into their application.

```
1. Developer registers on api.lurus.cn (or is invited by admin)
2. Developer creates an API token with desired model limits
3. Developer uses the token in their application:
   POST https://api.lurus.cn/v1/chat/completions
   Authorization: Bearer sk-xxxxx
   { "model": "gpt-4o", "messages": [...] }
4. Gateway routes request to optimal channel (by group, priority, weight)
5. If channel fails → automatic retry on another channel
6. Response streamed back to developer (SSE if stream=true)
7. Usage logged, quota deducted from token and user
```

**Success**: Developer gets consistent, reliable responses regardless of upstream provider.

### 4.2 Journey: Admin Manages Multi-Tenant Platform / 管理员管理多租户平台

**Persona**: Platform admin managing multiple business tenants.

```
1. Admin logs in via v1 session (root role)
2. Admin creates new tenant linked to Zitadel Organization
3. Admin configures tenant: max users, max quota, plan type
4. Tenant users authenticate via Zitadel OIDC (v2 API)
5. Tenant users are auto-mapped to lurus users
6. All tenant data automatically isolated via GORM plugin
7. Admin monitors tenant stats (users, channels, quota, billing)
```

**Success**: Each tenant operates independently with data isolation and no cross-tenant leakage.

### 4.3 Journey: User Subscribes and Uses Quota / 用户订阅并使用配额

**Persona**: User who needs regular LLM API access.

```
1. User views pricing page (/api/pricing)
2. User selects subscription plan (weekly/monthly/quarterly/yearly)
3. User pays via Stripe, Epay, or Creem
4. Subscription activates: user group upgraded, daily quota set
5. User consumes API → daily quota tracked by cron job
6. If daily quota exhausted → user falls back to lower group
7. Subscription auto-renews or expires based on settings
8. User can also redeem codes for bonus quota
```

**Success**: Seamless billing experience with clear quota visibility.

### 4.4 Journey: Task-Based AI Content Generation / 基于任务的 AI 内容生成

**Persona**: User generating images, video, or music via API.

```
1. User submits task (e.g., Midjourney image, Suno music, Kling video)
   POST /mj/submit/imagine or /v1/videos/generations
2. Task queued, ID returned immediately
3. Gateway polls upstream provider for task status
4. User checks task progress via API
5. Upon completion: result stored, quota deducted
6. User retrieves result
```

**Success**: Asynchronous content generation with transparent progress tracking.

---

## 5. Functional Requirements / 功能需求

### FR-1: LLM API Relay

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1.1 | Accept OpenAI-compatible request format (chat, embeddings, images, audio) | P0 |
| FR-1.2 | Route requests to configured channels based on model name, group, priority, weight | P0 |
| FR-1.3 | Support streaming responses via Server-Sent Events (SSE) | P0 |
| FR-1.4 | Implement per-provider adaptor pattern for request/response transformation | P0 |
| FR-1.5 | Auto-retry failed requests on alternative channels within same group or cross-group | P0 |
| FR-1.6 | Deduct quota from token and user after successful response | P0 |
| FR-1.7 | Log request metadata (model, tokens, latency, channel, user, cost) | P0 |
| FR-1.8 | Support model name mapping (aliases) per channel | P1 |
| FR-1.9 | Support pass-through mode (raw body forwarding) per channel or globally | P1 |
| FR-1.10 | Support Gemini native protocol relay | P1 |

### FR-2: Multi-Tenant Isolation

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-2.1 | Auto-inject `WHERE tenant_id = ?` on all GORM queries via plugin | P0 |
| FR-2.2 | Platform admin bypass tenant isolation for cross-tenant queries | P0 |
| FR-2.3 | Verify tenant ownership in payment webhooks (Stripe, Creem, Epay) | P0 |
| FR-2.4 | v1 API automatically uses "default" tenant for backward compatibility | P0 |
| FR-2.5 | v2 API extracts tenant from JWT claims (Zitadel Organization) | P0 |
| FR-2.6 | Tenant slug in URL path: `/api/v2/:tenant_slug/...` | P0 |

### FR-3: Authentication & Authorization

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-3.1 | Session-based authentication for v1 API | P0 |
| FR-3.2 | JWT (JWKS) verification for v2 API via Zitadel | P0 |
| FR-3.3 | PKCE + ID token nonce verification for OAuth security | P0 |
| FR-3.4 | Role-based access control: root > admin > user | P0 |
| FR-3.5 | API token authentication (Bearer sk-xxx) for relay endpoints | P0 |
| FR-3.6 | Rate limiting: global API, critical endpoints, model-level | P0 |

### FR-4: Billing

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-4.1 | Quota system: token-level and user-level quota tracking | P0 |
| FR-4.2 | Model ratio configuration (cost per input/output/1k tokens) | P0 |
| FR-4.3 | Multiple payment gateways: Stripe, Epay, Creem | P0 |
| FR-4.4 | Subscription lifecycle: create, activate, renew, expire, cancel | P1 |
| FR-4.5 | Daily quota cron for subscription users | P1 |
| FR-4.6 | Redemption code batch creation and redemption | P1 |

---

## 6. Non-Functional Requirements / 非功能需求

### NFR-1: Performance / 性能

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1.1 | Gateway processing overhead (p95) | < 50ms |
| NFR-1.2 | Streaming first-byte latency overhead | < 20ms |
| NFR-1.3 | Concurrent connections | 1000+ |
| NFR-1.4 | Channel cache refresh interval | Configurable (default 60s) |

### NFR-2: Reliability / 可靠性

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-2.1 | Monthly uptime | ≥ 99.5% |
| NFR-2.2 | Graceful shutdown timeout | 30 seconds |
| NFR-2.3 | Background task context cancellation | All tasks respond to ctx.Done() |
| NFR-2.4 | JWKS key refresh | Auto-refresh every 1 hour |
| NFR-2.5 | Panic recovery in goroutines | SafeGo/SafeGoWithContext wrappers |

### NFR-3: Security / 安全

| ID | Requirement | Implementation |
|----|-------------|----------------|
| NFR-3.1 | No credentials in code | K8s Secrets, env vars only |
| NFR-3.2 | Session secure flag | Reads SESSION_SECURE env; defaults true in release |
| NFR-3.3 | Cross-tenant data isolation | GORM plugin + webhook verification |
| NFR-3.4 | PKCE for OAuth | SHA-256 code_challenge, nonce verification |
| NFR-3.5 | JWKS race condition protection | Mutex + 30s min refresh interval |
| NFR-3.6 | API key masking in responses | Keys masked in list endpoints |
| NFR-3.7 | Turnstile captcha | Registration, login, critical endpoints |

### NFR-4: Scalability / 可扩展性

| ID | Requirement | Approach |
|----|-------------|----------|
| NFR-4.1 | Add new LLM provider | Implement Adaptor interface (14 methods, BaseAdaptor defaults) |
| NFR-4.2 | Add new task platform | Implement TaskAdaptor interface |
| NFR-4.3 | Multi-node deployment | Stateless app + shared PostgreSQL + Redis |
| NFR-4.4 | Schema isolation | PostgreSQL lurus_api schema |

### NFR-5: Observability / 可观测性

| ID | Requirement | Status |
|----|-------------|--------|
| NFR-5.1 | Structured logging via slog | ✅ Implemented |
| NFR-5.2 | Request ID propagation | ✅ Implemented |
| NFR-5.3 | CPU monitoring + pprof | ✅ Implemented |
| NFR-5.4 | Prometheus metrics endpoint | 📋 Planned (doc/decisions/observability.md) |
| NFR-5.5 | Distributed tracing (Jaeger) | 📋 Planned |

### NFR-6: Testing / 测试

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-6.1 | app/ layer coverage | ≥ 80% |
| NFR-6.2 | adapter/repo/ layer coverage | ≥ 60% |
| NFR-6.3 | adapter/handler/ layer coverage | ≥ 50% |
| NFR-6.4 | Tenant isolation tests | 25 tests (all passing) |
| NFR-6.5 | Test naming convention | Test<Subject>_<Method>_<Behavior> |

---

## 7. Technical Architecture Summary / 技术架构摘要

### 7.1 Project Structure / 项目结构 (Hexagonal Architecture)

```
lurus-api/
├── cmd/server/main.go              # Entry point (graceful shutdown, errgroup)
├── internal/
│   ├── domain/
│   │   └── entity/                  # Domain entities (struct definitions, value objects)
│   │       ├── channel.go           # Channel, ChannelInfo
│   │       ├── user.go              # User
│   │       ├── token.go             # Token
│   │       ├── tenant.go            # Tenant
│   │       ├── log.go               # Log + constants
│   │       └── ...                  # 15+ entity files (GORM tags preserved)
│   ├── app/                         # Use case orchestration (business logic)
│   │   ├── billing.go              # Billing operations
│   │   ├── quota.go                # Quota calculation
│   │   ├── channel_select.go       # Channel routing logic
│   │   ├── token_service.go        # Token CRUD
│   │   ├── user_service.go         # User CRUD
│   │   ├── passkey/                # Passkey authentication service
│   │   └── relay/                  # LLM relay engine
│   │       ├── text.go             # TextHelper, request processing
│   │       ├── claude.go           # Claude-specific handler
│   │       ├── gemini.go           # Gemini-specific handler
│   │       ├── task.go             # Task relay
│   │       └── helper/             # Stream scanner, model mapping
│   ├── adapter/
│   │   ├── handler/                # HTTP handlers (controllers, v1 + v2)
│   │   │   └── router/             # Route definitions
│   │   ├── middleware/             # Auth, rate-limit, tenant
│   │   ├── repo/                   # GORM repositories (data access)
│   │   │   ├── channel.go, channel_cache.go
│   │   │   ├── user.go, user_cache.go
│   │   │   ├── token.go, log.go
│   │   │   ├── tenant.go, tenant_plugin.go, tenant_context.go
│   │   │   └── topup.go, subscription.go, redemption.go
│   │   └── provider/              # AI vendor adaptors (30+)
│   │       ├── base_adaptor.go    # BaseAdaptor with defaults
│   │       ├── factory.go         # Adaptor factory (GetAdaptor)
│   │       ├── common/            # Shared relay utilities
│   │       ├── openai/            # OpenAI adaptor
│   │       ├── claude/            # Anthropic adaptor
│   │       └── ...                # 30+ more
│   ├── lifecycle/                  # Lifecycle manager, ticker tasks
│   └── pkg/                       # Shared utilities
│       ├── common/                 # SysLog, Redis, SafeGo, slog
│       ├── config/                 # Centralized config
│       ├── constant/               # API types, channel types, keys
│       ├── dto/                    # Data transfer objects
│       ├── logger/                 # Logger setup
│       ├── search/                 # Meilisearch integration
│       ├── setting/                # Ratio, model, operation settings
│       └── types/                  # Error types
├── web/                            # React frontend (Semi UI)
├── deploy/k8s/                     # Kubernetes manifests
└── doc/                            # Documentation
```

### 7.2 Data Flow / 数据流

```
Client Request
    ↓
[Gin Router] → [Middleware (adapter/middleware/): Auth + Rate Limit + Tenant Context]
    ↓
[Handler (adapter/handler/)] → validates input, extracts context
    ↓
[App Layer (app/relay/)] → selects channel, creates RelayInfo
    ↓
[Provider (adapter/provider/)] → transforms request to provider format
    ↓
[HTTP Client] → sends to upstream LLM provider
    ↓
[Response] → transforms back to OpenAI format
    ↓
[Stream/Buffer] → returns to client (SSE or JSON)
    ↓
[Post-processing] → log usage (adapter/repo/), deduct quota, update cache
```

### 7.3 Key Design Decisions / 关键设计决策

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Adaptor pattern for providers | Interface with 14 methods + BaseAdaptor (adapter/provider/) | Extensibility; add provider without touching core |
| Hexagonal architecture | domain/entity → app/ → adapter/ dependency direction | Clean separation, testable layers |
| Shared DB + tenant_id column | GORM plugin auto-injects WHERE (adapter/repo/) | Simpler than separate schemas; 2-person team |
| Session (v1) + JWT (v2) dual auth | Backward compatibility | Gradual migration; v1 continues working |
| Embedded React SPA | `web/dist` in Go binary | Single binary deployment |
| Redis for rate limiting | Token bucket via go-redis/v9 | Low latency, distributed support |
| Meilisearch for search | Separate deployment, async sync | Fast full-text search without DB load |

---

## 8. API Surface Summary / API 接口摘要

### 8.1 v1 API Endpoints (Production) / v1 API 端点

**Public (no auth)**:
- `GET /api/setup` - Check initialization status
- `POST /api/setup` - Initialize system
- `GET /api/status` - System status
- `GET /api/notice` - Announcements
- `GET /api/pricing` - Pricing page

**Authentication**:
- `POST /api/user/register` - Register
- `POST /api/user/login` - Login
- `GET /api/oauth/{provider}` - OAuth login (github, discord, oidc, linuxdo, wechat, telegram)
- `POST /api/user/login/2fa` - 2FA verification

**User Self-Service** (UserAuth):
- `GET/PUT/DELETE /api/user/self` - Profile CRUD
- `GET/POST /api/user/self/token` - Access token
- `POST /api/user/self/topup` - TopUp
- `POST /api/user/self/stripe/pay` - Stripe payment
- `POST /api/user/self/creem/pay` - Creem payment
- `GET/POST /api/user/self/2fa/*` - 2FA management
- `POST /api/user/self/passkey/*` - Passkey management
- `GET/POST /api/user/self/checkin` - Daily checkin

**Admin** (AdminAuth):
- `GET/POST/PUT/DELETE /api/user/admin/*` - User management
- `GET/POST/PUT/DELETE /api/channel/*` - Channel management
- `GET/POST/PUT/DELETE /api/token/*` - Token management
- `GET/POST/DELETE /api/redemption/*` - Redemption management
- `GET /api/log/*` - Log queries
- `GET /api/data/*` - Statistics

**Root** (RootAuth):
- `GET/PUT /api/option/*` - System options
- `GET/PUT /api/login-config/*` - Login configuration

**Relay** (TokenAuth):
- `POST /v1/chat/completions` - Chat completions
- `POST /v1/completions` - Text completions
- `POST /v1/embeddings` - Embeddings
- `POST /v1/images/generations` - Image generation
- `POST /v1/audio/speech` - TTS
- `POST /v1/audio/transcriptions` - STT
- `GET /v1/models` - List models
- `POST /mj/*` - Midjourney tasks
- `POST /suno/*` - Suno tasks
- `POST /v1/videos/*` - Video generation

### 8.2 v2 API Endpoints (Multi-Tenant) / v2 API 端点

**OAuth** (no auth):
- `GET /api/v2/:tenant_slug/auth/login` - Zitadel login redirect
- `GET /api/v2/oauth/callback` - OAuth callback
- `POST /api/v2/oauth/logout` - Logout
- `POST /api/v2/oauth/refresh` - Token refresh

**Tenant Routes** (Zitadel JWT):
- `GET/PUT /api/v2/:tenant_slug/user/me` - Current user
- `GET/POST/PUT/DELETE /api/v2/:tenant_slug/tokens/*` - Token CRUD
- `GET /api/v2/:tenant_slug/logs/*` - Log queries
- `GET/POST/PUT/DELETE /api/v2/:tenant_slug/channels/*` - Channel CRUD (admin)
- `GET/POST /api/v2/:tenant_slug/billing/*` - TopUp, Subscription
- `POST/GET/DELETE /api/v2/:tenant_slug/redemptions/*` - Redemption

**Platform Admin** (Root):
- `GET/POST/PUT/DELETE /api/v2/admin/tenants/*` - Tenant CRUD
- `GET/DELETE /api/v2/admin/mappings/*` - User mappings
- `GET /api/v2/admin/stats` - System statistics

---

## 9. Risks & Mitigations / 风险与缓解

| ID | Risk | Impact | Likelihood | Mitigation |
|----|------|--------|------------|------------|
| R-1 | Zitadel instance unavailable | Multi-tenant features blocked | Medium | v1 API continues working; Zitadel on CNPG with auto-backup |
| R-2 | Upstream LLM provider API change | Adaptor breakage | Medium | BaseAdaptor pattern isolates changes; monitoring alerts |
| R-3 | Cross-tenant data leakage | Critical security breach | Low | GORM plugin + webhook verification + 25 isolation tests |
| R-4 | Redis unavailability (office node) | Rate limiting + caching degraded | Medium | Tailscale mesh; graceful degradation |
| R-5 | Payment webhook spoofing | Financial loss | Low | Webhook signature verification + tenant ownership check |
| R-6 | Token quota race condition | Over-consumption | Low | Atomic DB operations + Redis-based rate limiting |
| R-7 | JWKS key rotation during request | Auth failures | Low | Auto-refresh every 1hr; retry with key refresh on miss |

---

## 10. Future Considerations / 未来规划

| Item | Priority | Description |
|------|----------|-------------|
| v1 API Deprecation | P2 | Gradual migration to v2; see `doc/decisions/v1-deprecation.md` |
| HA Deployment | P2 | Multi-replica stateless deployment; see `doc/decisions/ha-deployment.md` |
| Observability Stack | P2 | Prometheus metrics + Jaeger tracing; see `doc/decisions/observability.md` |
| API Key Authentication | P2 | Dedicated API key system (already partially implemented) |
| Webhook Notifications | P3 | Notify external systems on events (quota exhaustion, subscription changes) |
| Model Cost Analytics | P3 | Cost comparison dashboard across providers |
| Plugin System | P3 | Allow tenant-level custom logic (pre/post processing) |

---

## 11. Glossary / 术语表

| Term | Definition |
|------|-----------|
| **Channel** | An LLM provider configuration (API key, endpoint, models, priority) |
| **Adaptor** | Provider-specific implementation for request/response transformation |
| **Relay** | The process of forwarding client requests to upstream LLM providers |
| **Token** | API access key (sk-xxx) with quota, model limits, and group assignment |
| **Quota** | Usage credits (in token units) that are deducted per API call |
| **Group** | A logical grouping for channel selection and priority routing |
| **Tenant** | An isolated business entity mapped to a Zitadel Organization |
| **Tenant Slug** | URL-friendly identifier for a tenant (e.g., "lurus", "customer-a") |
| **JWKS** | JSON Web Key Set - public keys for JWT verification |
| **PKCE** | Proof Key for Code Exchange - OAuth security enhancement |
| **Model Ratio** | Cost multiplier for a specific model (input ratio + output ratio) |
| **BaseAdaptor** | Default implementation of the Adaptor interface (14 methods) |
| **TaskPlatform** | An async content generation platform (Midjourney, Suno, Kling, etc.) |
