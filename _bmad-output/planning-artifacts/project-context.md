---
project_name: 'lurus'
user_name: 'Anita'
date: '2026-02-02'
regenerated: '2026-02-02'
sections_completed: ['technology_stack', 'critical_rules', 'patterns', 'infrastructure', 'service_map', 'cicd', 'language_rules', 'framework_rules', 'pitfalls', 'usage_guidelines']
status: 'complete'
rule_count: 78
optimized_for_llm: true
---

# Project Context for AI Agents
# AI Agent 项目上下文

_This file contains critical rules and patterns that AI agents must follow when implementing code in the Lurus platform. Focus on unobvious details that agents might otherwise miss._

_本文件包含 AI Agent 在 Lurus 平台实现代码时必须遵循的关键规则和模式。重点关注 Agent 容易忽略的非显而易见的细节。_

---

## 1. Technology Stack & Versions / 技术栈与版本

### 1.1 Platform Shared / 平台共享

| Layer | Technology | Version | Notes |
|-------|-----------|---------|-------|
| Backend Runtime | Go | 1.25.1+ | CGO_ENABLED=0 mandatory |
| Container Orchestration | K3s | 1.34.3+ | Hybrid cloud (Tailscale VPN) |
| CI/CD | GitHub Actions → GHCR → ArgoCD | - | GitOps, no manual K8s apply |
| Database | PostgreSQL | 16+ | CNPG, schema isolation per service |
| Cache | Redis | 7+ | db number isolation per service |
| Messaging | NATS JetStream | latest | Stream prefix isolation |
| Object Storage | MinIO | latest | Bucket isolation |
| Mail Server | Stalwart | latest | RocksDB backend |
| Auth | Zitadel | latest | OIDC provider |

### 1.2 Service-Specific Stacks / 服务专属技术栈

#### lurus-www (Marketing Site)

| Technology | Version | Notes |
|-----------|---------|-------|
| Vue | 3.5.13 | Composition API + `<script setup>` ONLY |
| TypeScript | 5.7.2 | strict mode, target ES2022, module ESNext |
| Vite | 6.0.7 | Dev port 3001, esbuild minification |
| Tailwind CSS | 4.0.0 | **v4 CSS-first config (NOT tailwind.config.js)** |
| @tailwindcss/postcss | 4.1.18 | Replaces old `tailwindcss` PostCSS plugin |
| @tailwindcss/vite | 4.1.18 | Vite integration plugin |
| Vue Router | 4.5.0 | Client-side SPA routing |
| PostCSS | 8.4.49 | + Autoprefixer 10.4.20 |
| Path Alias | `@/*` → `./src/*` | Configured in tsconfig + vite.config |
| Package Manager | Bun (local dev) / npm (Docker build) | Docker uses node:20-alpine |
| Production Server | Nginx Alpine | Static files, SPA fallback, gzip |

⚠️ **Tailwind CSS v4 Critical Differences:**
- Config is CSS-based (`@theme {}` in CSS), NOT `tailwind.config.js`
- PostCSS plugin is `@tailwindcss/postcss`, NOT `tailwindcss`
- `@apply` still works but `@theme` replaces `theme.extend`
- Design tokens defined as CSS custom properties

#### lurus-lucrum (Quant Trading)

| Technology | Version | Notes |
|-----------|---------|-------|
| Next.js | 14.2.0 | App Router, Server Components default |
| Drizzle ORM | 0.45.1 | Type-safe PostgreSQL |
| Zustand | 4.5.7 | Global UI state |
| React Query | 5.60.0+ | Server state caching |
| Zod | 3.25.76 | Input validation |
| LangChain + LangGraph | 0.3.x / 0.2.x | AI multi-agent orchestration |
| lightweight-charts | 4.1.0 | K-line charting |
| Decimal.js | 10.6.0+ | **NEVER use JS numbers for money** |
| Vitest | 2.1.8 | ESM-native testing, 680+ tests |

#### lurus-api (Gateway)

| Technology | Version | Notes |
|-----------|---------|-------|
| Go + Gin | 1.9.1 | HTTP framework |
| GORM | latest | ORM for PostgreSQL |
| JWT | golang-jwt v5 | Authentication |

#### lurus-webmail (Email Platform)

| Technology | Version | Notes |
|-----------|---------|-------|
| Next.js + Nitro | latest | Web + Worker architecture |
| Package Manager | **pnpm** | Monorepo (NOT bun) |

#### lurus-switch (Desktop Client)

| Technology | Version | Notes |
|-----------|---------|-------|
| Go + Wails | 3.x | Desktop app framework |

---

## 2. Critical Implementation Rules / 关键实现规则

### Rule 1: Single Source of Truth / 唯一真理来源
- `lurus.yaml` is the **ONLY** configuration file for architecture
- Service definitions, ports, domains, infrastructure are all declared here
- NEVER hardcode infrastructure details in code; reference lurus.yaml

### Rule 2: Package Manager Discipline / 包管理器纪律
- `lucrum-web`: **Bun ONLY** (not npm, not yarn, not pnpm)
- `lurus-webmail`: **pnpm** (monorepo)
- `lurus-www`: **Bun**
- Go services: standard `go mod`

### Rule 3: Financial Calculation Safety / 金融计算安全
```typescript
// FORBIDDEN - floating point precision loss
const total = price * quantity; // JavaScript number

// REQUIRED - Decimal.js via FinancialAmount
import { FinancialAmount } from "@/lib/backtest/core/financial-math";
const total = new FinancialAmount(price).multiply(quantity);
```
- All monetary values MUST use Decimal.js
- China A-share 100-lot constraint enforcement
- Transaction cost modeling: commission + slippage + stamp duty

### Rule 4: Database Schema Isolation / 数据库 Schema 隔离
- PostgreSQL single instance, **schema-isolated** per service:
  - `lurus_api` → lurus-api service
  - `lucrum` → lurus-lucrum service
  - `identity` → auth/identity
  - `billing` → billing system
  - `webmail` → lurus-webmail service
- NEVER cross-reference schemas directly

### Rule 5: Redis DB Number Isolation / Redis 数据库号隔离
- `db:0` → lurus-api
- `db:1` → lucrum (production)
- `db:2` → rate limiting
- `db:3` → lucrum (staging)
- NEVER share db numbers between services

### Rule 6: File Encoding / 文件编码
- ALL files MUST be **UTF-8**
- CSV exports MUST include BOM (`\uFEFF`) for Excel compatibility

### Rule 7: Comment Language / 注释语言
- Code comments: **English ONLY**
- Documentation: **Chinese + English bilingual**
- UI text: Chinese primary, English secondary

### Rule 8: No AI Brand Leakage / 禁止 AI 品牌泄露
- NEVER include AI model names (Claude, GPT, etc.) in committed code
- NEVER include CLI tool names in committed code

### Rule 9: TDD Workflow / TDD 工作流
```
Requirement → Write Tests (RED) → Write Code (GREEN) → Refactor → Commit
```
- Coverage: app/ ≥ 80%, adapter/repo/ ≥ 60%, adapter/handler/ ≥ 50% — **all three MET as of 2026-08-02** (86.2% / 63.8% / 52.6%, CI coverage-gate run 30152927620) and now enforced by `go-ci.yml` thresholds 84/62/50 plus the `TestAppCoverageGate_HonestBaseline` lock
- Test naming: `Test<Subject>_<Method>_<Behavior>`
- FORBIDDEN: Writing code before tests
- Current achievement: backtest engine 85%+ coverage (680+ tests)

### Rule 10: Deployment Rules / 部署规则
- NEVER manually apply K8s manifests to production
- ALL deployments through GitOps: push to `main` → GitHub Actions → GHCR → ArgoCD
- Business workloads prefer master node (100.98.57.55)
- Container images: multi-stage build, scratch/alpine base
- Staging available: `lucrum-staging` namespace with isolated Redis (db:3)

---

## 3. Architecture Patterns / 架构模式

### Pattern 1: Independent Project Deployment / 独立项目部署
- Each business = independent project = independent Pod
- NO microservice splitting within a single business
- Services communicate via NATS JetStream or HTTP API

### Pattern 2: Server Components by Default / 默认服务端组件
- Next.js: Use Server Components unless hooks/events needed
- Add `'use client'` ONLY when client-side interactivity required
- Radix UI primitives for consistent styling

### Pattern 3: State Management Hierarchy / 状态管理层级
```
Zustand    → Global UI state (cross-page persistence)
React Query → Server state (caching, revalidation)
useState    → Local component state (forms, modals)
URL Params  → Shareable state (stock selection, dates)
```

### Pattern 4: Error Handling / 错误处理
```go
// Go: structured error returns
func (s *Service) Process(ctx context.Context) (*Result, error) {
    if err := validate(input); err != nil {
        return nil, fmt.Errorf("validation: %w", err)
    }
}
```
```typescript
// TypeScript: typed error codes (BT1XX-BT9XX)
if (error instanceof BacktestError) {
    console.error(`[${error.code}] ${error.message}`, error.context);
}
```

### Pattern 5: Data Validation Boundary / 数据验证边界
- Validate at system boundaries (user input, external APIs)
- Trust internal code and framework guarantees
- Use Zod schemas for TypeScript input validation
- Use GORM hooks for Go model validation

### Pattern 6: 3-Tier Persistence (Strategy Workspace) / 三层持久化
```
Auto-draft (3s debounce) → Zustand state → localStorage
```
- Prevents data loss on navigation
- Survives browser refresh
- Maintains 10 recent drafts

### Pattern 7: K-line Data Quality / K线数据质量
- ALWAYS validate before backtest: missing data, halts, price limits, anomalies
- Data source cascade: PostgreSQL DB → Eastmoney API → simulated fallback
- Track data source type in results for transparency

### Pattern 8: Workflow Orchestration / 工作流编排
```
WorkflowManager → StepExecutor → CacheStrategy
                                      ↓
                              Hash-based input matching
                              TTL-based expiration per step
                              Automatic invalidation
```
- Multi-step workflows managed via `src/lib/workflow/`
- Each step has deterministic inputs → cache-friendly
- Strategy development workflow: Input → Generate → Backtest → Validate

### Pattern 9: Hybrid Cache (Redis + In-Memory) / 混合缓存
```
Request → In-memory (LRU, <100ms) → Redis (TTL, <10ms) → Data Source
```
- Market data: 1hr TTL in Redis
- Backtest results: hash-based cache key
- Stock list: long TTL with manual invalidation
- Workflow step results: per-step TTL

### Pattern 10: Strategy Pipeline / 策略流水线
```
User Strategies:
  Natural language → AI Code Gen → Parameter Extraction → Backtest → Validate

Crawler Strategies:
  GitHub Discovery → Popularity Scoring → Format Conversion → Import
```
- Strategies grouped by source: `builtin` vs `user` vs `crawled`
- GET `/api/backtest/sector` returns grouped strategy lists

---

## 4. Infrastructure Topology / 基础设施拓扑

```
                    ┌──────────────────────────────────┐
                    │   Public: 43.226.46.164           │
                    └───────────────┬──────────────────┘
                                    │
┌───────────────────────────────────┼───────────────────────────────┐
│ Tailscale VPN (100.x.x.x)        │                               │
│                                   │                               │
│  ┌─────────────────┐   ┌─────────┴─────────┐   ┌──────────────┐ │
│  │ cloud-ubuntu-1   │   │ cloud-ubuntu-2     │   │cloud-ubuntu-3│ │
│  │ master 16C/32G   │   │ database 4C/8G     │   │worker 2C/2G  │ │
│  │ .57.55           │   │ .177.10            │   │.79.77        │ │
│  │                  │   │                    │   │              │ │
│  │ Traefik Gateway  │   │ PostgreSQL (CNPG)  │   │ lucrum-web   │ │
│  │ ArgoCD           │   │ Zitadel            │   │ lurus-www    │ │
│  │ Monitoring Stack │   │                    │   │ lurus-docs   │ │
│  │ lurus-api        │   │                    │   │              │ │
│  │ lucrum-staging   │   │                    │   │              │ │
│  └─────────────────┘   └────────────────────┘   └──────────────┘ │
│                                                                   │
│  ┌─────────────────┐   ┌────────────────────┐                    │
│  │ office-debian-2  │   │ office-win-1       │                    │
│  │ messaging        │   │ storage            │                    │
│  │ .110.73          │   │ .24.40             │                    │
│  │                  │   │                    │                    │
│  │ NATS JetStream   │   │ MinIO S3           │                    │
│  │ Redis            │   │ Backups            │                    │
│  │ lurus-newapi     │   │                    │                    │
│  └─────────────────┘   └────────────────────┘                    │
└───────────────────────────────────────────────────────────────────┘
```

---

## 5. Service Map / 服务地图

| Service | Domain | Port | Tech | DB Schema | Status |
|---------|--------|------|------|-----------|--------|
| lurus-api | api.lurus.cn | 8850 | Go+React | lurus_api | Production |
| lurus-lucrum | gushen.lurus.cn | 8870 | Next.js+Python | lucrum | Production |
| lurus-newapi | newapi.lurus.cn | 3000 | Go+React | newapi | Production |
| lurus-webmail | mail.lurus.cn | 3000/3001 | Next.js+Nitro | webmail | Beta |
| lurus-switch | - (desktop) | - | Go+Wails | - | Development |
| lurus-docs | docs.lurus.cn | 8880 | VitePress | - | Production |
| lurus-www | www.lurus.cn | - | Vue3+Vite | - | Production |

### Staging Environments

| Service | Namespace | Image Tag | Redis DB | Node |
|---------|-----------|-----------|----------|------|
| lucrum-web | lucrum-staging | staging | db:3 | cloud-ubuntu-1 (master) |

---

## 6. Git & CI/CD Pipeline / Git 与 CI/CD 流水线

```
Developer Push → GitHub
    ↓
GitHub Actions (build + test + docker build)
    ↓
GHCR (ghcr.io/lurustech/<service>)
    ↓
ArgoCD Sync (auto or manual)
    ↓
K3s Cluster Deployment (production or staging)
```

- Registry: `ghcr.io/lurustech/*`
- Namespace convention: `lucrum` (lucrum prod), `lucrum-staging` (lucrum staging), `lurus-system` (api), `mail` (webmail)
- Container base: `scratch` or `alpine` (multi-stage build)
- Go build flags: `CGO_ENABLED=0 GOOS=linux -ldflags="-s -w" -trimpath`

---

## 7. Language-Specific Rules / 语言特定规则

### 7.1 TypeScript (All Frontend Services)

**Strict Mode — Non-Negotiable:**
- `strict: true`, `noUnusedLocals`, `noUnusedParameters`, `noFallthroughCasesInSwitch`
- FORBIDDEN: `as any` type assertion — use type guards or Zod validation
- Pure type imports MUST use `import type { Foo }` (Tree-shaking)

**Module System:**
- Target: ES2022 / Module: ESNext / Resolution: bundler
- NEVER use CommonJS (`require`, `module.exports`)
- FORBIDDEN: barrel exports (`export * from './Foo'` in index.ts) — breaks Tree-shaking

**Import Conventions:**
```typescript
// ALWAYS use @/ path alias
import ChatInput from '@/components/Chat/ChatInput.vue'
import type { ChatMessage } from '@/types/chat'

// NEVER: deep relative paths (../../..)
```

### 7.2 Vue 3 Specifics (lurus-www)

**Component Pattern — ONLY this form:**
```vue
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

const props = defineProps<{ title: string; count?: number }>()
const emit = defineEmits<{ (e: 'update', value: string): void }>()

const isOpen = ref(false)
const displayTitle = computed(() => props.title.toUpperCase())
</script>

<template>
  <!-- ref auto-unwraps here: use isOpen, NOT isOpen.value -->
</template>

<style scoped>
/* Scoped styles REQUIRED */
</style>
```

**Reactivity Rules:**
- `ref()` — `.value` required in script, auto-unwrapped in template
- NEVER destructure ref (loses reactivity): `const { x } = myRef.value` ❌
- Use `toRefs()` when destructuring reactive objects
- All reactive state MUST be inside `<script setup>` scope

**v-model Shorthand (Vue 3.4+):**
- `defineModel()` is available and RECOMMENDED for v-model bindings
- Replaces manual `defineProps` + `defineEmits` + `update:modelValue` pattern
```vue
<script setup lang="ts">
// RECOMMENDED: defineModel() for v-model bindings
const modelValue = defineModel<string>({ required: true })
</script>
```

**Accessibility (a11y) — MANDATORY:**
- ALL interactive elements MUST have `aria-label` or `aria-labelledby`
- Form inputs MUST have associated `<label>` or `aria-describedby`
- Use semantic HTML (`<nav>`, `<main>`, `<section>`, `<button>`)
- Hidden helper text: use `sr-only` class for screen readers

**Testing Status:**
- ⚠️ lurus-www currently has NO test configuration (no vitest.config.ts)
- When tests are added, follow Vitest patterns from lurus-lucrum

**FORBIDDEN in lurus-www:**
- Options API (`export default { data(), methods }`)
- JSX/TSX — use `<template>` blocks
- Mixins — use composables (`use*.ts`)
- `this` keyword — does not exist in script setup
- React patterns: `useState`, `useEffect`, callback props

### 7.3 Vue vs React Quick Reference (Prevent Cross-Contamination)

| Concept | Vue 3 (lurus-www) | React (lurus-lucrum) |
|---------|-------------------|---------------------|
| State | `ref()` + `.value` | `useState()` |
| Side effects | `onMounted()`, `watch()` | `useEffect()` |
| Events | `defineEmits()` | Callback props |
| Conditional | `v-if` / `v-show` | `{cond && <C/>}` |
| Lists | `v-for` | `.map()` |
| Scoped styles | `<style scoped>` | CSS Modules |

### 7.4 Go (Backend Services)

**Build — MANDATORY:**
```bash
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o app
```

**Error Handling:**
```go
// Internal calls: wrap with %w (allows errors.Is/As unwrapping)
return fmt.Errorf("save entity %s: %w", entity.ID, err)

// Public API boundary: wrap with %v (prevents internal error leakage)
return fmt.Errorf("operation failed: %v", err)
```

**Context Rules:**
- ALL service/data layer functions MUST accept `ctx context.Context` as first param
- NEVER `context.Background()` in business logic (only main/tests/top-level)
- Long-lived goroutines: use `context.WithoutCancel(ctx)` to avoid premature cancellation

**Interface Design:**
- Accept interfaces, return structs
- Define interfaces at consumer site, NOT at implementation site
```go
// GOOD: interface defined where it's consumed
type UserRepository interface {
    GetByID(ctx context.Context, id string) (*User, error)
}
type UserService struct {
    repo UserRepository  // depends on interface
}

// BAD: interface defined next to implementation
type UserRepoImpl struct { db *gorm.DB }
type UserRepoInterface interface { ... }  // don't do this
```

**Package Boundaries:**
- ALL business logic MUST live inside `internal/` (compiler-enforced encapsulation)
- NEVER create public packages outside `internal/` to bypass this
- `internal/` sub-packages: no circular dependencies

**Naming:**
```go
// Full descriptive names — no abbreviations
func (s *Service) ProcessLLMRequest(ctx context.Context, req *Request) (*Response, error)
```

### 7.5 CSS & Styling (lurus-www Tailwind v4 + Vue Scoped)

**Scoped Style with Tailwind v4:**
```vue
<style scoped>
/* MANDATORY: @reference to enable Tailwind utilities in scoped context */
@reference "../../styles/main.css";

.card {
  @apply bg-cream-50 text-ink-900 rounded-sketchy;
}
</style>
```
- WITHOUT `@reference`, `@apply` will fail in scoped styles
- Global utility classes are defined in `src/styles/main.css`
- Design tokens: use CSS custom properties (`var(--color-cream-50)`)

### 7.6 Vue Performance Patterns (lurus-www)

- Read-only display data: use `shallowRef()` instead of `ref()` for large objects
- Avoid deep watchers: `watch(() => obj.field, cb)` over `watch(obj, cb)`
- NEVER use `v-for` + `v-if` on same element — filter with `computed` first

### 7.7 Environment & Runtime Rules

**Environment Variables (Service-Specific Prefixes):**
- lurus-www: `VITE_` (e.g., `VITE_API_URL`) — access via `import.meta.env`
- lurus-lucrum: `NEXT_PUBLIC_` (e.g., `NEXT_PUBLIC_API_URL`)
- NEVER access `process.env` in Vue — it does not exist in Vite

**Console Discipline:**
- `console.log` FORBIDDEN in production code
- `console.warn` acceptable for development-only warnings
- Use structured error handling, not console debugging

### 7.8 Go Testing Patterns

**Table-Driven Tests — REQUIRED style:**
```go
func TestUserService_GetByID_Scenarios(t *testing.T) {
    tests := []struct {
        name    string
        id      string
        want    *User
        wantErr bool
    }{
        {"valid user", "user-1", &User{ID: "user-1"}, false},
        {"not found", "missing", nil, true},
        {"empty id", "", nil, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := svc.GetByID(ctx, tt.id)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
            }
            // assert got matches tt.want
        })
    }
}
```

**Test Helpers — ALWAYS mark with `t.Helper()`:**
```go
// MANDATORY: t.Helper() so failure reports point to caller, not helper
func assertNoError(t *testing.T, err error) {
    t.Helper()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}
```

**Go Struct JSON Tags — MANDATORY:**
```go
// GOOD: explicit snake_case json tags
type User struct {
    ID        string    `json:"id"`
    UserName  string    `json:"user_name"`
    CreatedAt time.Time `json:"created_at"`
}

// BAD: missing tags (defaults to PascalCase in JSON)
type User struct {
    ID       string
    UserName string
}
```

---

## 8. Framework-Specific Rules / 框架特定规则

### 8.1 Vue Router (lurus-www)

**Route Structure:**
```typescript
// src/router/index.ts — lazy-loaded routes
const routes = [
  { path: '/', component: () => import('@/pages/Home.vue') },
  { path: '/pricing', component: () => import('@/pages/Pricing.vue') },
  { path: '/download', component: () => import('@/pages/Download.vue') },
  { path: '/about', component: () => import('@/pages/About.vue') },
]
```
- ALL page routes use dynamic `import()` for code splitting
- Pages live in `src/pages/` (PascalCase: `Home.vue`, `Pricing.vue`)
- Components live in `src/components/<Category>/` (e.g., `Chat/`, `Hero/`, `Portal/`)
- External redirects handled in `src/main.ts`, NOT in router config
- New pages MUST be registered in `src/router/index.ts` with lazy `import()`

### 8.2 Vite Configuration (lurus-www)

**Dev Server:**
- Port: 3001 (NOT default 5173)
- API Proxy: `/api` → `https://api.lurus.cn` (avoids CORS in development)
- ⚠️ Proxy only works in dev — production Nginx serves static files directly

**Build Optimization:**
- Manual chunks: Vue + Vue Router bundled as `vendor` chunk
- Source maps: DISABLED in production
- Minification: esbuild (NOT terser)

### 8.3 lurus-www Design System

**Color Tokens (CSS Custom Properties):**
```
Cream (backgrounds):  cream-50 #FFFDF7 → cream-300 #F5E6B8
Ink (text):          ink-900 #2C2416 → ink-100 #D4CCC0
Accent:              ochre #C9A227

Product colors:
  product-api: #6B8BA4 (slate blue)
  product-lucrum: #7D8B6A (sage green)
  product-switch: #C67B5C (terracotta)
  product-docs: #C9A227 (ochre gold)

Portal categories:
  portal-academic: #8B6B7D    portal-finance: #7D8B6A
  portal-ai: #6B8BA4          portal-engineering: #C67B5C
  portal-medical: #4A8B6A     portal-legal: #8B4A6A
```

**Typography (Golden Ratio φ = 1.618):**
- phi-base: 16px/1.75 — body text
- phi-xl: 26px/1.6 — section headers
- phi-2xl: 42px/1.4 — page titles
- phi-3xl: 68px/1.3 — hero text
- Fonts: Caveat (handwritten accent) + Noto Sans SC (Chinese body)

**Spacing (Fibonacci Sequence):**
- fib-1: 5px, fib-2: 8px, fib-3: 13px, fib-4: 21px, fib-5: 34px, fib-6: 55px, fib-7: 89px

**Hand-drawn UI Style:**
- Border radius: `sketchy` (asymmetric: `4px 15px 8px 12px / 12px 8px 15px 4px`)
- Cards: `card-sketchy` class with paper texture SVG filters
- Buttons: `btn-hand` class with sketchy border radius
- Animations: `float` (6s vertical bob), `wiggle` (3s rotation)

### 8.4 lurus-www Component Architecture

```
src/components/
├── Layout/          # Navbar, Footer (persistent across routes)
├── Hero/            # Landing page hero section
├── Chat/            # AI chat sidebar (7 sub-components)
│   ├── AIChatSidebar.vue    # Container, toggle panel
│   ├── ChatHeader.vue       # Model selector, controls
│   ├── ChatInput.vue        # Auto-resize textarea
│   ├── ChatMessages.vue     # Message list container
│   ├── ChatMessageBubble.vue # Individual message
│   ├── ChatQuickPrompts.vue # Preset prompt buttons
│   ├── NetworkStatusBanner.vue
│   └── TypingIndicator.vue
├── Features/        # Product feature grid
├── Products/        # Product showcase cards
├── Pricing/         # Subscription plan cards
├── Download/        # Platform download section
└── Portal/          # External resource links (6 categories × 8 links)
```

**Composables Pattern:**
```
src/composables/
├── useAIChat.ts         # Chat orchestration (debounce, mutex, retry)
├── useChatApi.ts        # API calls with error handling
├── useChatPersist.ts    # LocalStorage message persistence
└── useNetworkStatus.ts  # Online/offline detection
```
- Composables encapsulate stateful logic, components handle presentation
- Chat system uses optimistic updates (immediate UI → confirm/rollback)

### 8.5 Next.js App Router (lurus-lucrum)

- Server Components by DEFAULT — add `'use client'` ONLY for interactivity
- Layouts in `app/layout.tsx` — shared navigation, providers
- Data fetching: Server Components fetch directly, Client Components use React Query
- Radix UI primitives for accessible component foundation

### 8.6 API Integration Patterns (lurus-www)

**Standard Response Format:**
```typescript
interface ApiResponse<T> {
  success: boolean
  data?: T
  message?: string
}

// ALWAYS check success before using data
const res = await fetch(`${import.meta.env.VITE_API_URL}/api/subscription/plans`)
const json: ApiResponse<SubscriptionPlan[]> = await res.json()
if (!json.success) {
  // handle error, show json.message to user
}
```

**URL Construction:**
- Development: Vite proxy `/api/*` → `https://api.lurus.cn` (auto, no CORS)
- Production: `VITE_API_URL` env var (default `https://api.lurus.cn`)
- ALWAYS use `import.meta.env.VITE_API_URL`, NEVER hardcode domain

**Chat API — No Authentication Required:**
- Chat endpoints are public (visitor pre-registration experience)
- Do NOT add Authorization headers to chat requests
- Response: `{ success: true, data: { reply: string, model: string } }`

### 8.7 Design System Usage Rules

**Color Token Semantics (use these, NOT Tailwind defaults):**

| Token | Use For |
|-------|---------|
| `cream-50` | Page background |
| `cream-100` | Card backgrounds |
| `cream-200` | Hover/active states |
| `cream-300` | Borders, dividers |
| `ink-900` | Primary text, headings |
| `ink-700` | Secondary text |
| `ink-500` | Muted text, captions |
| `ink-300` | Placeholder text |
| `ink-100` | Disabled text |
| `ochre` | Primary accent, CTAs, focus rings |
| `product-*` | Service-specific branding |
| `portal-*` | Link category colors |

- FORBIDDEN: Tailwind default palette (`bg-blue-500`, `text-gray-700`, etc.)
- FORBIDDEN: Hardcoded hex values (`bg-[#FFFDF7]`) — use tokens
- FORBIDDEN: Custom asymmetric border-radius — use `rounded-sketchy` or `rounded-sketchy-btn`

**Animation Rules:**
- `float` / `wiggle` — decorative elements ONLY (icons, badges, accents)
- NEVER animate functional elements (buttons, inputs, navigation)
- New animations must follow existing easing (`ease-in-out`) and duration patterns

**Component Placement:**
- New components MUST go into a category directory under `src/components/<Category>/`
- NEVER create loose files at `src/components/` root level
- New pages MUST be registered in `src/router/index.ts` with lazy `import()`

**Responsive Design:**
- Use Tailwind responsive prefixes (`sm:`, `md:`, `lg:`) — NEVER fixed `px` widths
- Mobile-first approach: base styles = mobile, add breakpoints for larger screens

**External Links:**
- ALL external links: `target="_blank" rel="noopener noreferrer"`
- Portal link data is static in `src/data/portalLinks.ts`, not API-fetched

---

## 9. Common Pitfalls / 常见陷阱

**Platform-Wide:**
1. **Using npm instead of bun** → Dependency conflicts, 10-20x slower
2. **JavaScript floating point for money** → Use Decimal.js FinancialAmount
3. **Cross-schema DB queries** → Violates isolation principle
4. **Manual K8s apply in production** → Must use GitOps pipeline
5. **Forgetting Windows file handle cleanup** → Tests leave locked files
6. **Not including UTF-8 BOM in CSV** → Excel Chinese garbled text
7. **Committing AI model names** → Team policy violation
8. **Staging Redis db:3 vs prod db:1** → Cross-environment data leakage

**lurus-www Specific:**
9. **Tailwind v4 scoped without `@reference`** → `@apply` silently fails
10. **Using Tailwind default palette** (`bg-blue-500`) → Must use custom tokens (cream/ink/ochre)
11. **Hardcoded API URL** → Must use `import.meta.env.VITE_API_URL`
12. **Vue ref destructuring** → Loses reactivity, use `toRefs()`
13. **Options API or JSX in Vue** → Use Composition API + `<template>` only
14. **Components at root level** → Must place in `src/components/<Category>/`
15. **Missing ARIA attributes** → All interactive elements need accessibility labels

**lurus-lucrum Specific:**
16. **Missing 'use client' directive** → Server Component errors with hooks
17. **Hardcoding market hours** → Use `isMarketOpen()` utility
18. **Ignoring K-line validation** → Bad data corrupts backtest results
19. **Workflow step caching without hash** → Stale results on parameter changes
20. **Crawler without rate limiting** → GitHub API throttling / IP ban

---

## 10. Usage Guidelines / 使用指南

**For AI Agents / AI Agent 使用指南:**
- Read this file BEFORE implementing any code in the Lurus platform
- Follow ALL rules exactly as documented — when in doubt, prefer the more restrictive option
- Identify which service you are working on and follow its service-specific rules
- Pay special attention to the Vue vs React distinction (Section 7.3)
- Use the design system tokens for lurus-www (Section 8.7), never Tailwind defaults

**For Humans / 人工维护指南:**
- Keep this file lean and focused on agent needs
- Update when technology stack changes (version bumps, new dependencies)
- Review quarterly for outdated rules
- Remove rules that become obvious over time (e.g., if agents consistently follow them)
- Archive to `doc/archive/` when major restructuring occurs

**Last Updated / 最后更新:** 2026-02-02
