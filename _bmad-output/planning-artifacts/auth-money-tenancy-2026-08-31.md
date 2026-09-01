# 登录/注册/充值/多租户 — 架构拍板、问题台账、测试轮 (2026-08-31)

## 1. 拍板:登录注册放 platform,newhub 只做 relying party

**结论:C 端与 B 端的账号生命周期(注册/登录/改密/MFA/社交登录)全部由
2l-svc-platform(identity.lurus.cn)承载;newhub 不做也不再新增任何本地凭证
面。** 这不是新决策,是确认既有架构并把它修到真正能用:

- newhub 侧证据:users 表**没有密码列**(entity/user.go:12 "Auth is delegated
  to the OIDC provider"),无 register/login handler,/login 与 /register 前端
  路由都只是跳转 identity.lurus.cn(OidcRedirect.jsx)。
- platform 侧证据:Zitadel 基座 + 完整生命周期(注册/密码/忘记密码/邮箱验证/
  magic-link/SMS/微信/MFA/企业 SSO)+ org 三层层级(enterprise→distributor→
  end_user,DB 触发器防环)+ 钱包与六家支付 provider。
- 可靠性论证:凭证面只有一个(攻击面/审计面唯一),钱包身份与登录身份同源
  (account_id 贯穿),避免重演 #109 跨租户顶替类缺陷。

**分工边界**:platform = 身份+钱包+支付+组织;newhub = 令牌/租户授权/计量/
渠道路由。桥 = `lurus_session` cookie → `POST /api/v2/auth/zita-bootstrap`
(自动建号,`lurus_account_id` 链接)。

## 2. 钱流闭环(充值对接的系统图)

```
支付(identity.lurus.cn/topup, epay/alipay/stripe…)
  → platform 钱包 (billing.wallets, LUC)
    → 路径A: LUC→LUT 兑换 (platform 调 newhub /internal/currency/exchange)
             → newhub 本地 quota → 任意令牌可用
    → 路径B: BILLING_UNIFIED 令牌 (IdentityAccountID>0)
             → relay 直接 gRPC PreAuth/Settle 扣钱包
B 端: 租户 credit pool (CREDIT_POOL_REQUIRED=enforce, 无池 402)
```

## 3. 今日修复(全部 live 验证)

| # | 缺陷 | 修复 | 验证 |
|---|---|---|---|
| F1 | **platform 注册永久 500**(migration 012 删错约束名 `uni_accounts_email`,真名 `accounts_email_key` 存活;有一行 email='' 后所有免邮箱注册 23505→500,且每次孤儿化一个 Zitadel 用户) | live DROP CONSTRAINT + platform migration 126(幂等)+ 修复畸形行 184 | 注册探针 500→**201**(account 185 完整) |
| F2 | **浏览器用户计费全线 503 "platform account not linked"**(bootstrap 把 identity_account_id 写 session,OIDCAuth session fallback 从不拷进 context) | 两分支注入(session 值优先,users.lurus_account_id 兜底)| 变异 2 红;⏳ 部署后 live 复验 |
| F3 | **bridge 自动建号 0 配额**,新用户首次调用即 402(OIDC 路径发 quota.new_user_quota=10000,bridge 路径被 Insert 的 option 默认 0 覆盖) | autoCreateBridgedUser 对齐同一政策 | 变异红;⏳ 部署后 live 复验 |

## 4. 问题台账(整理自历史 + 本轮,按 owner 分)

**外部硬阻塞(上线关键路径)**
- E1 🔴 **支付商户凭证缺位**(EPAY_PARTNER_ID/KEY 未配;微信/支付宝直连需真实
  商户证书)——充值真钱进不来,全链只差这一步。mock-epay 可测协议链。

**platform 侧(代码在,需修/需部署)**
- P1 Register 失败不回滚 Zitadel 用户(每次 500 孤儿化一个 IAM 身份)——
  migration 126 后此路径不再触发,但防御性回滚值得补。
- P2 UpsertByZitadelSub 把 loginName 塞 email 列(非邮箱登录时造畸形行)。
- P3 钱包 numeric(14,4) + cents 迁移(105 dual-write)进行中;<0.0001 LB 截零。
- P4 platform 私仓 Actions 全灭 → 部署走手工出包配方。

**newhub 侧(owner 决策)**
- N1 控制台令牌不带 IdentityAccountID(统一计费不覆盖控制台令牌)——前置 =
  P3 钱包精度;接通前令牌走本地 quota + LUC→LUT 兑换,是安全的过渡态。
- N2 zita 桥用户全部落 default 租户(B 端租户要走 :tenant_slug OIDC 路径或
  root 手工建租户);org→tenant 自动映射是否开(OIDC_AUTO_CREATE_TENANT)= owner。
- N3 旧直连 OIDC 与 zita 桥双轨并存(迁移债,旧轨仍可达)。
- N4 v2 充值按钮硬编码 200 元 + alipay(应做金额选择器,读 platform
  payment_settings 预设)。
- N5 Passkey 配置面是死代码(fork 遗留,零 handler)——platform 侧同样未实现。

**已修/已收口(引用)**
- 跨租户顶替(#109)、/metrics 暴露(#106-108)、缓存计价双向错(#115)、
  错误日志缺口(4bb786b8)、限流成功计数(#113)、轮换 CAS(#114)。

## 5. 本轮测试(正交设计:按 seam 一次覆盖,不做 N×M)

| Journey | 覆盖的 seam | 结果 |
|---|---|---|
| A. C 端全链(live prod):注册→登录→bootstrap→计费→建令牌→relay→登出→重放 | platform 注册/登录、跨域 cookie、自动建号、账号链接、计费桥、令牌、配额、会话失效 | 注册 500(→修→201)· 登录/建号/令牌/登出/重放失效 PASS · 计费 503(F2)· relay 402(F3)|
| B. B 端/租户(UAT):33 Playwright(租户 CRUD/隔离/scope 拒绝/审计导出/钱路)| 租户管理、令牌 scope、审计、控制台钱路 | **33 passed / 1 skip,与基线逐字一致** |
| C. 回归(hermetic)| middleware+handler 全包 | EXIT=0;新增 4 测试,4/4 变异红 |
| D. 部署后复验 | F2/F3 的 live 判据 | ⏳ 新鲜 journey 重跑:summary 200、quota=10000、relay 放行 |

**杠杆设计原则**:每个 seam 只在一条 journey 里测一次;负面各测一处(无 cookie
401 / 登出后 401 / 无池 402 已有 Go 锁);其余组合交给 hermetic 层。

## 5b. 2026-09-01 workflow 修复轮(P1/P2/N2/N4/N5 全部上线)

台账逐项重验(8 侦察 agent,raw = `ledger-recon-raw-2026-09-01.json`)后四车道实现,
全部 live 验证:

| # | 交付 | live 判据 |
|---|---|---|
| P1 | 注册失败补偿删除 IdP 用户(仅本次新铸+仅 commit 前分支,失败只记日志) | platform `8ba39e8a`,Core CI/CD 自动部署 `main-72d4c12`,常规注册回归 201/200/200 |
| P2 | UpsertByZitadelSub net/mail 分类,非邮箱 loginName 落 username 列 | platform `3b64fb55`,同上;变异红×2 |
| N2 | 租户邀请码(migration 032):root 签发一次性码,bootstrap `?invite=` 仅首登消费 | 探针:新用户落 `probe-invite` 租户、invite 行转 consumed、同码重放回落 default 照常登录 |
| N4 | checkout 选择器 + typed error + **pay_url 字段错配**(成功也报失败) | 400 真消息透传 live;顺带揪出↓ |
| N4b | 🔴 **Idempotency-Key 要 header 不是 body——checkout 从未端到端通过**,旧 503 一刀切把它埋了整个历史 | 修后探针:400 从 "requires Idempotency-Key header" 推进到 "provider not configured"(=E1,链上只剩商户凭证) |
| N5 | passkey 死面整体移除(settings 剧场+/api/status 投影+secure-verification 引用) | `/api/status` 零 passkey 键;缺失锁+legacy options 行容忍锁 |

**驳回/不动**:P3/N1 维持 owner 结论(钱包 14,4 有意设计);N3 侦察结论「legacy OIDC
零消费者」被否决——hub 域浏览器 SSO(08-30 实证)就走它,文档化保留,不加投机 flag。

**新入账**:
- 🟡 platform `TestWalletRepo_BalanceEqualsSumAfterMixedConcurrent` Integration 常年红
  (≥6 连红横跨无关提交,钱包并发不变量)→ owner,常年红毁信号。
- 🟡 tenant_invites 表 GORM 先建 ⇒ `created_at` 无 DB default(SQL 032 声明了
  DEFAULT NOW() 但 live 表没有;app 写路径不受影响,raw SQL 插入须显式供值)。
- 🟡 v2_provision.go 已解析 :tenant_slug 却仍建 default 用户(既有缺口,N2 注释标记)。
- ✅ platform Actions 已复活且 Core CI/CD 是 push-to-deploy(set image+rollout+/sli
  验证+自动回滚)——「P4 手工出包」台账项作废。

## 6. 运维记录

- UAT bridge token 已轮换(两次泄露:昨日 Playwright 工件 + 今日代理 base64),
  UAT deployment 已滚动。
- platform repo 有他人 WIP:autostash 回放冲突,完整内容在 stash `611dd2e0`,
  未动;该会话需自行 pop 解决。
