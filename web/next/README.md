# newhub 新控制台 (web/next)

基于上游开源前端(TypeScript + React 19 + TanStack Router/Query + shadcn(base-ui) + Tailwind 4 + rsbuild)裁剪而来,与旧控制台(`web/`,Semi UI)并存。挂载在 `/next/`,AGPL 版权头保留。

## 命令(一律 bun)

```bash
cd web/next
bun install
bun run dev         # 开发,/api /v1 /pg 代理到 http://localhost:3000
bun run typecheck   # tsgo -b
bun run lint        # oxlint
bun run test        # vitest
bun run build       # 产物 -> web/dist/next
```

构建顺序:**先旧后新**。旧控制台 `cd web && bun run build` 会清空 `web/dist`,再 `cd web/next && bun run build` 写入 `web/dist/next`。Go 通过 `web/embed.go` 的 `all:dist` 一并嵌入,`web-router.go` 对 `/next/*` 返回 `next/index.html`(无该产物时回落旧 index)。

## 登录与会话

新控制台没有登录页。任何接口 401 或未登录 -> `window.location` 跳 `/login?redirect=<当前 /next/... 路径>`,旧登录页登录后按 `redirect` 回跳(只接受站内相对路径,见 `web/src/helpers/loginRedirect.js`)。开发时 cookie 按主机共享(不分端口),先在旧控制台登录再打开新控制台即可。

## API 约定(`src/lib/api.ts`)

- 同源、带 cookie。信封 `{success, data, message, error_code}`,`unwrap` 失败抛 `ApiError`(含 `status`、`code`)。
- 租户接口一律走 `tenantApi.*` -> `/api/v2/~/...`(`~` = 本会话的租户,**不要存或传 slug**)。
- 当前用户/角色:`currentUserQueryOptions`(`GET /api/v2/~/user/me`),角色门控见 `components/layout/nav.ts` 的 `minRole`(与旧控制台 HFShell 同一量纲:admin=10,root=100)。
- 金额:一律走 `src/lib/money.ts`(`useMoneyConfig` + `formatQuota`),单位价取自 `/api/status` 的 `quota_per_unit`,**禁止硬编码 500000**;状态未返回时显示 `--`。

## 路由

文件路由在 `src/routes/`(`routeTree.gen.ts` 由构建生成并入库)。`_authenticated/` 下的页面共用外壳。页面实现放在 `src/features/<name>/`,路由文件只做挂载。

## i18n 约定

- 语言只有 `zh`(默认)与 `en`;词条 key 就是英文原句,缺译回落为 key 本身。
- 通用词条:`src/i18n/locales/{zh,en}.json`。
- **每个 feature 自带词条**:`src/features/<name>/locales/{zh,en}.json`,由 `src/i18n/resources.ts` 自动汇总(rsbuild 的 `import.meta.webpackContext`),新增 feature 不需要改任何共享文件。不同 feature 对同一 key 给出不同译文时开发态会告警。
- 并行开发时只改自己 feature 目录下的词条文件,避免合并冲突。
