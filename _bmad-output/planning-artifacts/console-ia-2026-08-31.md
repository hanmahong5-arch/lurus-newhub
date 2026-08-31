# Console Information Architecture — diagnosis, target, roadmap

Date: 2026-08-31 · Status: Phase 0 shipped, Phase 1+ pending owner review
Scope: v2 console (`/console/v2/*`, `HFShell.jsx`) + legacy remnants + top bar.

## 1. Diagnosis (verified against `HFShell.jsx` / `App.jsx` as of 2026-08-31)

The complaint "左页签/上页签不够系统、不够结构化" decomposes into six concrete
defects, ordered by user harm:

1. **One 14-item flat "platform · admin" section** mixed four unrelated jobs:
   upstream routing (Channels/Models/Pricing), tenant governance
   (Tenants/Users/Projects/Redemption/Model limits), analytics
   (Cost intelligence/Model performance), and platform ops
   (Gateway health/Admin settings). No scanning order, no hierarchy.
2. **Zero client-side role gating in v2** — every logged-in end user saw the
   entire admin section (items 403 on click). The legacy sidebar gates with
   `isAdmin()/isRoot()`; the v2 port dropped it.
3. **Fake chrome**: hardcoded demo badges (`5` tokens, `$241` billing,
   `8` channels, `54` models — wrong for every real user), and a ⌘K search
   button with **no click handler**.
4. **Account "Settings" (sessions revoke) sat in the admin section** — it is
   user-scope; every member needs it.
5. **Two shells at once**: 7 legacy pages (`/console/personal`, `topup`,
   `midjourney`, `task`, `openrouter-sync`, `user`, `setting`) still render the
   old Semi-UI chrome. Worst case: **top-up — a money path — is only reachable
   through the legacy shell**; v2 Billing shows balance but offers no way to
   pay.
6. **Routable-but-unlisted surfaces**: `cmdk`, `flows`, `design-system`,
   `states`, `variants` (hi-fi demo screens) reachable only by URL.

## 2. Principles applied (benchmark: OpenRouter / OpenAI Platform / Stripe-class consoles)

- **Group by job-to-be-done, not by backing table.** A section answers "what am
  I here to do", not "which table does this CRUD".
- **Progressive disclosure by role.** An end user's rail shows only their
  world; operator surfaces appear with operator role. Server authz remains the
  real gate; the rail is honesty about what you can do.
- **Primary metrics before secondary.** Spend / remaining quota / error rate /
  p99 are first-paint; channel internals, breaker states, per-model analytics
  are operator drill-downs, never top-level for end users.
- **Truth in chrome.** No fabricated numbers, no dead controls. A badge is
  real data or absent; a button acts or does not exist. (Same bar the console
  already applies to the disabled MJ-logs placeholder.)

## 3. Phase 0 — shipped 2026-08-31 (no route changes, ids/paths stable)

New rail (5 sections; `minRole: 10` hides the last three from end users):

| Section               | Items                                                                                 | Audience |
| --------------------- | ------------------------------------------------------------------------------------- | -------- |
| workspace             | Dashboard · Playground · Chat                                                         | everyone |
| my account            | Tokens · Usage & logs · MJ/Task logs (deferred) · Billing · **Settings (moved here)** | everyone |
| routing & models      | Channels · Models · Pricing                                                           | admin+   |
| governance            | Tenants · Users (admin) · Projects · Model limits · Redemption · Audit trail          | admin+   |
| operations & insights | Gateway health · Cost intelligence · Model performance · Admin settings               | admin+   |

Also: fake badges removed; ⌘K button navigates to `/console/v2/cmdk`;
breadcrumbs on all 11 affected pages follow the new sections; user identity
read moved to lazy init so the rail is role-correct on first paint.

## 4. Roadmap (owner review; effort ≈ S/M/L)

- **P1 (M) — close the money-path split.** Port top-up into v2 Billing (one
  page, "add funds" tab/CTA) and retire `/console/topup`. Until then v2 users
  literally cannot pay without falling into the legacy shell.
- **P1 (S) — retire the remaining legacy personal pages** (`personal` →
  v2 Settings; `midjourney`/`task` → un-defer the MJ/Task logs entry as one
  v2 log tab). Two shells is the single biggest "不系统" signal left.
- **P2 (M) — real ⌘K overlay.** Global keydown → palette as an overlay (the
  `cmdk` page already has groups/actions); today's page-navigation is honest
  but not the benchmark interaction.
- **P2 (S) — real badges.** Tokens count and channels-in-error from data
  already fetched by their pages (shared SWR cache), or stay absent. Never
  static strings.
- **P2 (S) — fold `openrouter-sync`, `user`, `setting` (root)** into the v2
  admin area (operations & insights) and drop the legacy sidebar entirely.
- **P3 (L) — persona split.** If reseller/tenant-admin becomes a real
  audience: two rails (member console vs operator console) behind one shell,
  mode-switched next to TenantSwitcher — the Stripe "test/live + account
  switcher" shape. Pricing likely becomes a public/user surface (legacy top
  bar already treats it that way).
- **P3 (S) — demo surfaces** (`flows`/`design-system`/`states`/`variants`):
  keep routable, add `?dev` guard or move under `/console/v2/dev/*` so they
  never leak into customer navigation.

## 5. Metric hierarchy (what is primary)

- **Primary (every user, dashboard first paint):** total spend, remaining
  quota, error rate, p99 latency, requests. (Current v2 dashboard order is
  already correct; keep.)
- **Secondary (admin, one click down):** per-model cost/performance, channel
  health/breaker state, tenant consumption, audit events.
- **Tertiary (operator, on demand):** circuit-breaker per-replica state,
  routing savings analysis, schema/gateway internals.

Non-goals: renaming routes (breaks bookmarks/e2e), touching legacy pages'
internals in Phase 0, any backend change.
