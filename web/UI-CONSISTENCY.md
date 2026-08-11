# v2 Console — UI 清单 + 体验一致性审计

> 范围:**仅 `/console/v2/*` hi-fi 页面**。老 v1 Semi UI 管理页(User/Setting/Redemption/
> Task/Midjourney/Personal 等)与开发者展示页(DesignSystem/States/Variants)**不在本轮审计内**。
> 真源:路由表 `web/src/App.jsx`、外壳 `web/src/components/hifi/HFShell.jsx`、
> 设计 token `web/src/styles/hifi-tokens.css`。
> 最近核实:2026-06-09(grep + 读源码)。

---

## 1. 全页面 UI 清单

每页一行,非抽样。`HFShell` 列由 `rg "HFShell" web/src/pages/v2/**/index.jsx` 核实;
`样式系统` 列由 `rg "@douyinfe/semi-ui" web/src/pages/v2 -g '!*.test.jsx'` 核实;
`加载态` / `空·错误态` 列由源码 grep 核实(见各页对应文案 / `NotAvailable` / `data-testid`)。

### 生产页(本轮审计对象)

| Route                                 | File                               | HFShell  | 样式系统                                                          | 加载态                 | 空 / 错误态                                        |
| ------------------------------------- | ---------------------------------- | :------: | ----------------------------------------------------------------- | ---------------------- | -------------------------------------------------- |
| `/console/v2/dashboard`               | `Dashboard/index.jsx`              |    ✅    | hi-fi                                                             | `Loading…`(muted 文本) | muted 空态                                         |
| `/console/v2/log`                     | `Log/index.jsx`                    |    ✅    | hi-fi                                                             | `Loading…`             | `NotAvailable`(TTFT 等无后端列)+ muted 空态        |
| `/console/v2/channel`                 | `Channel/index.jsx`                |    ✅    | hi-fi                                                             | `Loading…`             | `NotAvailable`(QPS/P50 等)+ muted 空态             |
| `/console/v2/token`                   | `Token/index.jsx`                  |    ✅    | hi-fi                                                             | `Loading…`             | muted 空态                                         |
| `/console/v2/playground`              | `Playground/index.jsx`             |    ✅    | hi-fi                                                             | `加载中…`              | `暂无预设`                                         |
| `/console/v2/cmdk`                    | `CommandPalette/index.jsx`         |    ✅    | hi-fi                                                             | 静态(无后端)           | n/a                                                |
| `/console/v2/models`                  | `Models/index.jsx`                 |    ✅    | hi-fi                                                             | `加载中…`              | `暂无模型`(`data-testid=models-empty`)             |
| `/console/v2/chat`                    | `Chat/index.jsx`                   |    ✅    | hi-fi                                                             | 流式响应内联态         | 会话空态                                           |
| `/console/v2/tenants`                 | `Tenants/index.jsx`                |    ✅    | hi-fi                                                             | `Loading…`             | muted 空态                                         |
| `/console/v2/pricing`                 | `Pricing/index.jsx`                |    ✅    | hi-fi **(本轮修复:去 Semi `Spin`)**                               | actions 区 `加载中…`   | `暂无数据`                                         |
| `/console/v2/redemption`              | `Redemption/index.jsx`             |    ✅    | hi-fi                                                             | `加载中…`              | `暂无兑换码`(`data-testid=redemption-empty`)       |
| `/console/v2/billing`                 | `Billing/index.jsx`                |    ✅    | hi-fi                                                             | 内联 loading           | muted 空态                                         |
| `/console/v2/settings`                | `Settings/index.jsx`               |    ✅    | hi-fi                                                             | `Loading…`             | `billing-empty-state` 等                           |
| `/console/v2/flows`                   | `Flows/index.jsx`                  |    ✅    | hi-fi                                                             | 内联态                 | 引导到 Channel 的空态                              |
| `/console/v2/account-disabled`        | `AccountDisabled/index.jsx`        | ❌(故意) | hi-fi **(本轮修复:去 Semi `Empty`/`Button`/`Typography` + 插画)** | n/a(终端页)            | **本身即终端错误页**                               |
| `/console/v2/admin/users`             | `Admin/Users/index.jsx`            |    ✅    | hi-fi                                                             | `Loading…`             | muted 空态                                         |
| `/console/v2/admin/settings`          | `Admin/Settings/index.jsx`         |    ✅    | hi-fi                                                             | `Loading…`             | `NotAvailable`(stats 无数据)+ muted 空态           |
| `/console/v2/admin/cost-intelligence` | `Admin/CostIntelligence/index.jsx` |    ✅    | hi-fi                                                             | `Loading…`             | `NotAvailable` + per-table `empty` 文案 + 403 守卫 |

> `AccountDisabled` 不套 `HFShell` 是**正确的**:账户已被禁用不应渲染导航 / 租户切换。它改用一个
> `.hf` scope 的居中错误卡片(`.hf` 根才能激活 `--hf-*` 变量与共享类——`HFShell` 自身也靠
> `<div className='hf …'>` 根)。这是合法例外,不是脱离设计系统。

### 开发者展示页(不审 — 仅设计预览,无业务后端)

| Route                       | File                     | HFShell | 备注                               |
| --------------------------- | ------------------------ | :-----: | ---------------------------------- |
| `/console/v2/design-system` | `DesignSystem/index.jsx` |   ❌    | 设计 token / 组件画廊预览          |
| `/console/v2/states`        | `States/index.jsx`       |   ✅    | empty/loading/error/modal 状态演示 |
| `/console/v2/variants`      | `Variants/index.jsx`     |   ✅    | 组件变体演示(`TweaksPanel`)        |

### legacy v1(本轮不迁 — 仍 Semi UI)

`/console/*` 下的 v1 管理页(User / Setting / Redemption / Task / Midjourney / Personal 等)
继续使用 Semi UI。它们不在 `/console/v2/*` 命名空间内,**不属本轮范围**;迁移记为后续 PR。

---

## 2. 设计系统契约速查

### HFShell(`web/src/components/hifi/HFShell.jsx`)

外壳 = sidebar(brand + 三段 NAV + 底部 `TenantSwitcher`)+ topbar(crumbs + actions + 主题切换 + logout)。

| Prop       | 类型      | 说明                                                        |
| ---------- | --------- | ----------------------------------------------------------- |
| `active`   | string    | 高亮的 NAV item id;省略时由 pathname 自动推断(`PATH_TO_ID`) |
| `crumbs`   | string[]  | topbar 面包屑;最后一段加粗                                  |
| `actions`  | ReactNode | topbar 右侧操作区(放页面级按钮 / **加载提示**)              |
| `children` | ReactNode | 页面正文,渲染进 `.hf-body`                                  |

NAV 三段:`workspace`(Dashboard/Playground/Chat)· `my account`(Tokens/Usage&logs/
MJ·Task logs〔禁用占位〕/Billing)· `platform · admin`(Channels/Models/Tenants/Pricing/
Redemption/Settings/Cost intelligence/Users(admin)/Admin settings)。

### `--hf-*` 变量与共享类(`web/src/styles/hifi-tokens.css`,全部 scope 在 `.hf` 下)

- **颜色**:`--hf-bg/-paper/-elev/-sunken`、`--hf-rule/-rule-strong`、`--hf-ink/-ink-2/-ink-3/-ink-4`、
  `--hf-accent/-accent-2`、`--hf-ok/-warn/-err/-info`。亮 / 暗主题由 `[data-theme='dark']` 切换。
- **字体**:`--hf-display`(Fraunces)、`--hf-sans`(Inter Tight)、`--hf-mono`(JetBrains Mono)。
- **文本类**:`.display` `.mono` `.lbl` `.muted` `.faint` `.strong` `.acc`。
- **控件类**:`.btn`(+ `.primary/.ghost/.acc/.sm`)、`.field`、`.kbd`、`.tag`(+ 语义色)、
  `.dot/.live-dot`、`.pill`、`.spark`。
- **容器类**:`.panel` `.panel-paper` `.panel-sunken`、`table.t`、`.hf-page-head`、`.hf-numeral`。

**禁用**:Semi UI、Tailwind 工具类、内联字体声明。

### Canonical 页面骨架

参考 `Admin/CostIntelligence/index.jsx`:

```
<HFShell active=… crumbs={[…]} actions={…}>
  <div className='hf-page-head'> lbl + <h1> + sub </div>
  <div style={{ padding: … }}>
    <div className='panel'> … table.t / grid … </div>
  </div>
</HFShell>
```

### 空态语义区分(重要)

- **`NotAvailable`**(`components/hifi/NotAvailable.jsx`):结构性「**无后端**」——该值无法产出(没有聚合 /
  存储 endpoint),显式标 `n/a` + reason,**绝不造假值**(守 CLAUDE.md §4.1 ⑥)。
- **`.muted` 空态 / `暂无…` 文案**:数据真的为空(还没有行),不是缺后端。

二者不可混用:缺后端用 `NotAvailable`,空数据用 `.muted` 空态。

---

## 3. 差距清单(按可执行性排序)

### 本轮修复(本 PR)

1. **`Pricing` 去 Semi `Spin`** — 删 `import { Spin }`;删包住正文的 `<Spin spinning={loading}>`/
   `</Spin>` 两层;加载态改为 `HFShell` `actions` 内 `加载中…`(对齐 Dashboard / CostIntelligence)。
   已有 `filteredPricing.length === 0 && !loading → 暂无数据` 空态守卫,保留。
2. **`AccountDisabled` 改回 hi-fi** — 删 `@douyinfe/semi-ui` + `@douyinfe/semi-illustrations` +
   全部 Tailwind 类;改为 `.hf` scope 的居中 `.panel` 错误卡片,按钮用 `.btn`/`.btn primary`,
   保留 `useTranslation` 两动作(mailto / zita-logout)与全部 i18n key;**不套 HFShell**(终端页)。

### 记录但本轮不做(未来收敛,不在本 PR)

> 明示登记,避免 silent 漏报。下列为 DRY / 一致性债务,均**不阻塞**本轮一致性不变量。

- **`QUOTA_PER_USD` 在多文件重复**(~8 处常量):应抽到单一公共模块。
- **`quotaToUSD` / `formatUSD` 多份实现**:USD 换算逻辑散落,应统一。
- **4 套时间格式化**:各页自行 format 时间戳,应统一为一个 helper。
- **`InlineEdit` 在 Token / Channel 各自定义**:两份近似实现,应抽公共组件。
- **加载文案不统一**:部分页用英文 `Loading…`,部分用中文 `加载中…`;应统一一种(并走 i18n)。

---

## 4. 一致性不变量

> 所有 v2 **生产页**零依赖 Semi UI。可 grep 验证:

```bash
cd web && rg "@douyinfe/semi-ui" src/pages/v2 -g '!*.test.jsx'
```

**期望输出:空**(无任何匹配)。本 PR 之前有 2 个匹配(`Pricing`、`AccountDisabled`);
修复后应为零。新增 / 修改 v2 生产页时此不变量必须保持。

### 4.1 helper 层间接依赖(2026-07-09 补,ui-industrial-audit fixPlan #2)

上面的 grep 只查页面**直接** import `@douyinfe/semi-ui` —— 漏掉了 `helpers/utils.jsx`
的 `showError`/`showSuccess`/`showWarning`/`showInfo`/`showNotice`。这些是全站唯一的
toast 反馈函数,`helpers/utils.jsx:20` 本身仍 `import { Toast } from '@douyinfe/semi-ui'`
(legacy `/console/*` v1 页面继续用它),但当调用方 `window.location.pathname` 落在
`/console/v2/*` 时会改走 `.hf` scope 的 `components/hifi/HfToast.jsx`(`hfToast.*`),
不再弹出 Semi UI 的 ant-design 风格 toast。

核验这条不变量不能只 grep import,要连带确认路由分支仍然存在:

```bash
cd web && rg "isV2Route\(\)" src/helpers/utils.jsx
```

**期望输出**:`showError`/`showWarning`/`showSuccess`/`showNotice` 每个分支都有一次
`isV2Route()` 判断包住 `Toast.*` 调用。若未来有人把某个 show\* 函数改回无条件调用
`Toast.xxx`,这条 grep 会因命中数下降而失败——这就是本条不变量的核验口径。
