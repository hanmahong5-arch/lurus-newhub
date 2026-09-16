# Cycle 10 — console completion: wire the backends we already shipped

**Trigger.** The owner compared `hub.lurus.cn/console/v2/chat` against `newapi.lurus.cn` and said
"这项目照着 newapi 这个差远了,功能都不全". They are right about what they saw. This plan is about
*why* they saw it, which is not what "功能都不全" usually means.

---

## 1. The finding that reframes the cycle

Most of what looks missing in the v2 console is **already built and already serving data**. The
console does not read it, and in four places ships a banner telling the user the capability does not
exist.

Measured 2026-09-16, against this repo at `3978dd0e` and the production database on R6:

| Capability | Backend | v2 console |
|---|---|---|
| Per-day, per-model usage series | `GET /api/data/self/` → `QuotaData{ModelName, CreatedAt, TokenUsed, Count, Quota}`. Aggregator runs by default (`common.DataExportEnabled = true`). **Production `quota_data` holds 1614 rows, 2026-06-25 → 2026-09-16, 3 models, 5 users.** | **zero consumers** — `grep -rn "api/data" web/src` is empty |
| Announcements / FAQ / API-info panels | `GET /api/status` already returns `announcements`, `faq`, `api_info` and the three `*_enabled` flags (`internal/adapter/handler/misc.go:178-210`) | v2 Dashboard reads none of them (legacy `NoticeModal`/`NotificationButton` do) |
| Uptime panel | `GET /api/uptime/status` (`api-router.go:32`), returns `[]` when unconfigured | no v2 consumer |
| Per-model enable/disable | `GET/PUT/DELETE /api/v2/admin/tenants/:id/model-allowlist` — observe/enforce modes, `internal/app/tenantpolicy` | **zero console consumers.** The Models page instead renders `WIPBanner reason="per-model enable/disable deferred to v3"` |
| Chat completions | `POST /api/v2/:tenant_slug/chat/send` runs a real multi-turn completion through in-process loopback to `/v1/chat/completions` | the page **works**, and renders `⚠ WORK IN PROGRESS — content is design-mock only` above it |

That last row is the one the owner screenshotted. The banner is not merely conservative, it is
**false**: the content is not design-mock, the chat really runs. What is missing there is
persistence and streaming, which is a different and much smaller claim.

This is the same defect class as cycle 8's Log routing panel ("UI 等 API 的联动缺陷") and cycle 9's
L6 (`upstream_request_id` filter sent to a route that ignored it). Third occurrence. It gets a
structural gate this cycle (§5).

## 2. What the owner's two screenshots actually differ by

Three separate causes, and only the third is a feature gap:

1. **Role.** The hub screenshot is `lurus_23`, role 1. The newapi screenshot is root. Production
   newhub has exactly one role-100 account and no role-10 accounts, so an ordinary user sees the
   8-item user nav by design. Not a defect; worth stating so it stops being re-litigated.
2. **Shipped placeholders.** 10 `WIPBanner` renders across 4 pages (Chat ×2, Flows ×4, Models ×2,
   Settings ×2), all unconditional. This is what makes the product read as unfinished regardless of
   what the backend does.
3. **Real gaps** vs New API's console (`2b-svc-newapi/web/default/src/routes`, 57 routes): sectioned
   dashboard (`dashboard/$section` = overview / model analytics / user analytics), chat persistence
   (`chat/$chatId`), a `profile` page, `subscriptions`, seven `system-settings` sections against our
   one flat admin page, and `pricing/$modelId`.

We are also **ahead** on surfaces New API has no counterpart for: tenants, projects, audit trail,
permission grants, cost intelligence, model performance, gateway health, model limits, background
tasks, diagnostics.

## 3. Correction to the record

On 2026-09-16 I told the owner the v2 console "把成熟的 New API 控制台踢出默认入口,老页面还活着只是
没有入口". That was half wrong and I am correcting it here rather than quietly dropping it. Re-read
of `web/src/App.jsx`: `/console/models`, `/console/channel`, `/console/token`, `/console/playground`,
`/console/deployment`, `/console/redemption`, `/console/topup`, `/console/log` and `/console` all
**redirect into v2** — the migration is further along than I said. Only seven legacy routes still
render legacy components: `/console/user`, `/console/setting`, `/console/personal`,
`/console/midjourney`, `/console/task`, `/console/openrouter-sync`, `/console/chat/:id?`. Of those,
`midjourney` and `task` are reachable *only* by typing the URL — the v2 nav item that would lead
there is `disabled: true` with the title "not available in v2 yet" (`HFShell.jsx`).

## 4. Lanes

Binding order L1 → L7. One shared tree, serialized. **L7 is the sole owner of `web/src/App.jsx` and
`web/src/components/hifi/HFShell.jsx`** — no other lane edits either file; a lane that needs a route
or a nav entry states it in its report and L7 lands it. Locale JSON: each lane adds keys to BOTH
`en.json` and `zh.json`, only under its own `translation.console.<page>.*` subtree (the keys are
**nested**, not flat — cycle 9 §8 got this wrong once).

### L1 — Dashboard: render the three months of data we already store
Wire the v2 Dashboard to `GET /api/data/self/` (daily per-model `Quota`/`TokenUsed`/`Count`), and to
the `/api/status` fields it already receives but discards. Add, each gated on its own enabled flag
and rendering **nothing** when the flag is off or the payload is empty: usage trend over time, model
consumption distribution, announcements panel, FAQ panel, API-info panel, uptime panel.
Keep the existing realtime KPI strip.
*Oracle:* a test that drives the page with a recorded `/api/data/self/` payload and asserts a
non-empty series renders; a second that asserts each panel is absent when its `*_enabled` flag is
false. **Not** a snapshot test.
*Watch:* `/api/data/self/` rejects a span > 30 days (`usedata.go:41`) with `success:false` and HTTP
200. Pick the default window inside that bound and handle the refusal as an empty state, not a toast.

### L2 — Models: per-model enable/disable, against the allowlist that exists
Replace both `WIPBanner`s. The allowlist write path is root-gated
(`/api/v2/admin/tenants/:id/model-allowlist`), and the Models page is `UserAuth` — **do not widen
that authz to make the UI convenient.** Put the enable/disable UI on the admin page that already
owns the neighbouring capability, `admin/model-limits` (rename its surface to cover availability),
and on the Models page replace the banners with a real per-row state read from the allowlist plus,
for role ≥ 100, a link to the admin page. For a user who cannot change it, show the state, not a
warning.
*Oracle:* observe vs enforce mode must be visibly different in the UI — in observe mode a
"disabled" model still answers, and saying otherwise would be a new lie. Assert both renderings.

### L3 — Chat: delete the false banner, then earn the rest
(a) Remove `console.chat.wip_persistence` and `console.chat.wip_retry`. The page works; the banner
says it does not.
(b) Persistence: migration **038** `chat_sessions` + `chat_messages` (PG-only, idempotent, id
reserved in the root ledger before any SQL is written), `GET/POST/PATCH/DELETE
/api/v2/:tenant_slug/chat/sessions[/:id]` under `UserAuth` + `TenantSlugGuard`, ownership fail-closed
(another user's session id and a nonexistent one return the same 404). Wire the existing sidebar.
*Explicitly out of scope, stated in a doc comment and NOT in a banner:* SSE streaming. Say why:
loopback SSE needs a second hop that re-multiplexes upstream chunks.
*Oracle:* a REAL-CHAIN test through the mounted v2 router proving the IDOR 404 symmetry, plus a
round-trip create → list → fetch → delete.

### L4 — Flows: wire the one wizard that has a backend, delete the three that do not
`newChannel` needs credential validation, a connection test and model discovery — all three already
exist (`POST …/channels`, `POST …/channels/:id/test`, `GET …/channels/:id/upstream-models`). Wire it.
`incident` and `retry` are static screens with no backend and no plan; **remove the tabs** rather
than ship them behind a warning. `newToken` already works.
*Oracle:* the wizard's submit must be proved against the real handler chain, not a mocked `API.post`.

### L5 — Settings: resolve both banners, keep neither
- **Team**: account and membership lifecycle belongs to platform (`lurus/CLAUDE.md`; newhub is a
  relying party). Retire the section and link to platform identity. Do not build a `tenant_member`
  table here.
- **Notifications**: decide and act. Either a minimal real subscription backed by the alerting path
  that already exists, or retire the section. A disabled toggle over placeholder event names is the
  one outcome this lane may not ship.
*Oracle:* `Settings/index.test.jsx` currently asserts both banners are PRESENT (lines ~233, ~247).
Those assertions invert this cycle — update them deliberately and say so in the diff, do not delete
them.

### L6 — MJ / async task logs: the v2 page behind the disabled nav item
New page over `GET /api/task/self/` (which already carries the `project_id` / `request_id` filters
added in cycle 8 L10) and `GET /api/mj/self/`. New files only; the route and the nav un-disable are
L7's to land.
*Oracle:* the filters must reach the server — assert the request URL, not just that rows render.

### L7 — Navigation & reachability (sole owner of `App.jsx` + `HFShell.jsx`)
Land L6's route, un-disable "MJ / Task logs", give `Flows` a nav entry now that it is real, and
resolve `profile`: either a v2 page or an honest labelled link to `/console/personal`. Decide
`/console/openrouter-sync`, which today has no nav entry at all — link it or retire it.
*Oracle:* extend the existing route-contract test so a nav entry pointing at an unregistered route
fails the build, and so does a `disabled: true` nav item whose destination route exists.

## 5. The structural gate this cycle owes

Three cycles in a row have shipped a backend whose console never read it. Add one gate, in Go, that
fails on the fourth:

`TestConsoleReadsEveryUserFacingEndpoint` — enumerate the v2/v1 routes that project user-facing data
(an explicit, commented list, not a heuristic), and assert each appears in `web/src`. A route added
without a consumer must either get one or be added to a **reasoned** exclusion list naming its
intended consumer. First run will name today's gaps; that list is the scope document.

Pair it with a frontend gate: **zero `WIPBanner` renders in `web/src/pages/v2`** at the end of this
cycle, enforced by a test, so the component can only come back deliberately.

## 6. Not doing

New API's `subscriptions` (hard-blocked on the epay merchant credentials, same blocker as invoice
PDF), the seven-way `system-settings` split (our single admin page is honest, just flatter),
`pricing/$modelId`, chat SSE streaming, and any `tenant_member` store in newhub.

## 7. Verification

Local gates, lint and the full suite never concurrent:

```
go vet ./... && go build ./...
go test -short -count=1 -p 2 ./...
go test -run '<structural set + TestConsoleReadsEveryUserFacingEndpoint>' -p 2 ./...
golangci-lint run --new-from-rev=origin/main ./internal/... ./cmd/...
cd web && bun run lint && bunx eslint src --ext .js,.jsx && bun run test && bun run build
```

`bun test` is bun's own runner and reports hundreds of false failures against this vitest suite —
always `bun run test`. `bun run eslint`'s glob finds zero files in this shell; use the `bunx` form.
`-race` and the coverage ratchet are CI-only.

Every behaviour change mutation-proved: named oracle red on the reverted behaviour, green on a
byte-identical restore. Commit before mutating.

**UAT probes** per lane, on `test-newhub.lurus.cn` / NodePort 30851, via
`POST /api/v2/bridge/exchange?token=…&user_id=N`. Production stays read-only. UAT's `quota_data` may
be near-empty — seed it there, never in production, and revert.

## 8. Owner items carried in

O1 (enrol TOTP for prod root, then flip `SECURE_VERIFICATION_REQUIRE_ENROLLMENT`), O2 (where the
netdata host's `SEND_CUSTOM` sender delivers; the three cycle-9 alarms are installed but not loaded
— `netdatacli reload-health` hangs and the fix needs an `obs-netdata` restart that blinds monitoring
for every service on R6), O3 (a real vendor channel on UAT — still blocks the cycle-9 L4/L5 live
probes), O4 (per-vendor context-tier values).

New this cycle: **O6** — L5 Notifications is a product decision, not an engineering one. Minimal
real subscription, or retire the section?
