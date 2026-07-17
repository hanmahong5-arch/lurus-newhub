# Lurus 计费系统说明

> 最后更新 2026-02-25 · 维护:Lurus 技术团队

## 系统架构

核心组件: 充值系统 (`topups`) · 订阅系统 (`subscriptions`, `subscription_plans`) · 额度管理 (`users.quota`, `users.daily_quota`) · 扣费系统 (`logs`) · 兑换码 (`redemptions`)。多产品按 `tenant_id` 隔离。

## 支持的支付方式

| 支付方式 | 代码标识 | 地区 | 手续费 | 状态 | 说明 |
|---------|---------|------|--------|------|------|
| Stripe | `stripe` | 全球 | 2.9% + $0.30 | ✅ 生产可用 | Checkout Session + Webhook 验签 |
| 易支付 (Epay) | `epay` | 中国 | 自定义 | ✅ 生产可用 | 聚合支付,走支付宝/微信/银联通道 |
| Creem | `creem` | 全球 | 3.5% | ✅ 生产可用 | 固定产品定价,Webhook 验签 |
| 支付宝 | `alipay` | 中国 | 0.6% | ❌ 仅 OAuth 登录 | 无支付层,直连需企业资质 |
| 微信支付 | `wechat` | 中国 | 0.6% | ❌ 仅 OAuth 登录 | 无支付层,直连需企业资质 |

> 国内支付:通过**易支付**已可走支付宝/微信通道 (中间商,约 1-2% 手续费)。官方直连 (0.6%) 需企业营业执照 + 平台审核。

环境变量: `STRIPE_SECRET_KEY` / `STRIPE_WEBHOOK_SECRET`;`EPAY_URL` / `EPAY_PID` / `EPAY_KEY`;`CREEM_API_KEY` / `CREEM_SECRET`。

## 计费模式

- **预付费 (充值)**: 先充后用,按消耗扣费。换算 `QuotaPerUnit = 500,000`(每元 quota 单位,可按汇率配),即 ¥1 = 50万 quota。API: `GET /api/user/topup` → `POST /api/user/pay {amount, payment_method}`;回调 `GET /api/user/epay/notify`、`POST /api/pay/stripe`、`POST /api/pay/creem`。
- **订阅制**: 周期性额度 + 日限额,到期前 24h 自动从余额扣费续费 (`auto_renew=true`)。默认套餐:

  | 套餐 | 价格 | 总额度 | 日限额 | 有效期 |
  |------|------|--------|--------|--------|
  | 周付 | ¥19.9 | 500万 | 50万/天 | 7天 |
  | 月付 | ¥59.9 | 5000万 | 100万/天 | 30天 |
  | 季付 | ¥149.9 | 2亿 | 200万/天 | 90天 |
  | 年付 | ¥499.9 | 无上限 | 500万/天 | 365天 |

- **混合模式 (推荐)**: 订阅 + 充值并存。`user.quota` 是统一余额池 (订阅激活加 `TotalQuota`,充值加 `Money*QuotaPerUnit`);`daily_quota` 控制每日上限,次日凌晨 Cron 重置 `daily_used=0`。

## 收费流程

充值页选金额(档位 ¥50/100/200/500)+ 支付方式 → 创建订单 (`INSERT topups status=pending`) → 调支付 API 取链接 → 用户支付 → Webhook 回调 (验签 + 防重放) → `UPDATE topups SET status=success` + `UPDATE users SET quota=quota+amount` → 跳回控制台。

安全措施:
1. **订单防重放**: 唯一 `trade_no = fmt.Sprintf("USR%dNO%s%d", userId, RandomString(6), Unix())`;Webhook 校验 `topup.Status != TopUpStatusPending` 则忽略 (幂等)。
2. **签名验证**: Stripe `webhook.ConstructEvent(body, signature, secret)`;易支付 `client.Verify(params)`;Creem HMAC-SHA256。
3. **租户隔离**: Webhook 校验 `user.TenantId == topup.TenantId`。
4. **行级锁**: `tx.Clauses(clause.Locking{Strength:"UPDATE"}).Where("trade_no=?",tradeNo).First(&topUp)`（GORM v2 惯用法;旧的 `Set("gorm:query_option","FOR UPDATE")` 在 v2 下静默失效不发锁,禁用）。

## 多产品计费能力评估

当前模式支持状态(均 ✅ 完整): 按 Token/Quota 计费 (`PreConsumeQuota`+`PostConsumeQuota`)、按次 (`logs.request_count`)、包月/包年 (`subscriptions` + Cron)、一次性充值 (`topups` + Webhook)、充值档位折扣 (`AmountDiscount` map)、分组差异化定价 (`TopupGroupRatio`)、订阅自动续费 (`subscription_cron.go`,每小时 Cron)。

**对 AI 网关自身:✅ 可支撑** (条件:不依赖官方直连支付宝/微信)。**对多产品计费中台:❌ 不能直接支撑** — 限制: 计费单位与 Token 强耦合、无通用扣费 API、无产品维度隔离(`tenant_id` 是组织维度)、无统一账单视图、无退款系统、无发票/收据。

**推荐路径 B**:lurus-api 专注 AI 网关计费(现有,生产就绪);其他产品各自接入支付 SDK(共享 Zitadel SSO)。**长期**:独立 `lurus-billing` 计费中台(统一账单/退款/对账),所有产品通过内部 API 调用。**路径 A(可选)**:扩展 lurus-api 为中台,需引入 `product_id` 字段 + 通用扣费 API + 通用计量单位,改动大,Epic 级规划。

## 配置指南

- **支付渠道**(Root → 系统设置 → 支付配置): Stripe Webhook 端点 `https://api.lurus.cn/api/pay/stripe`(事件 `checkout.session.completed` / `payment_intent.succeeded`);易支付 Notify URL `https://api.lurus.cn/api/user/epay/notify`,Return URL `https://api.lurus.cn/console/log`。
- **充值档位**(系统设置 → 充值配置): `{"amount_options":[50,100,200,500], "amount_discount":{"500":0.95,"1000":0.90}, "min_topup":10}`。
- **订阅套餐**(Root → 订阅套餐管理): `{"code","name","days","price","currency","daily_quota","total_quota","base_group","fallback_group","enabled"}`。
- **自动续费** (`autoRenewalProcessorWithContext`,每小时): 检查 24h 内到期 + `auto_renew=true`;从 `quota` 扣 `plan.Price*QuotaPerUnit` 同时补 `plan.TotalQuota`(原子事务);余额不足记警告日志不续费(邮件通知 TODO)。

## 常见问题

- **Q1 多久到账?** Stripe / 易支付 / Creem 即时 (Webhook 1-5s);银行转账人工核对。
- **Q2 自动续费?** `auto_renew=true` 到期前 24h 从 quota 扣;`false` 失效不续。逻辑见 `subscription_cron.go: processOneAutoRenewal` — `renewalCost := int(plan.Price * common.QuotaPerUnit)`;余额足:`DB.Transaction` 内 `quota += plan.TotalQuota - renewalCost` + `expires_at += plan.Days`;不足:记日志待邮件 (TODO)。
- **Q3 退款?** 当前**无退款模块**(计划在 lurus-billing)。临时:用户提工单 → 管理员在支付后台手动退 → API 扣减用户 quota(用户管理 → 调整额度)。
- **Q4 防恶意充值退款?** (需手动实施) 冷静期(`time.Since(topup.CreateTime)<7*24h && user.UsedQuota>topup.Amount*0.5` 拒退);风控审核(单笔 >¥1000 / 24h >3 次 / 充值后 1h 用 >80%)。
- **Q5 企业对公转账?** 支持,管理员手动:工单+凭证 → 验证到账 → 补单(充值管理输入订单号)或调整额度。
- **Q6 大客户定制计费?** 兑换码批量生成、直接调整额度、或内部订阅 `POST /api/v2/{tenant}/internal/subscriptions {user_id, plan_code, days, reason}`(不走支付)。
- **Q7 多产品对账?** 按 `tenant_id` 统计(注意:`tenant_id` 是组织/产品维度,非独立产品账户维度;统一账单待 lurus-billing):
  ```sql
  -- 收入: SELECT tenant_id, COUNT(DISTINCT user_id) paying_users, SUM(money) revenue_cny, COUNT(*) order_count
  --        FROM topups WHERE status='success' AND create_time>='2026-02-01' GROUP BY tenant_id;
  -- 消耗: SELECT tenant_id, SUM(quota) quota_consumed, COUNT(*) api_calls
  --        FROM logs WHERE created_at>='2026-02-01' GROUP BY tenant_id;
  ```

## 计费能力矩阵

✅ 按量充值 (Stripe/易支付/Creem) · 包月订阅 (四档可定制) · 订阅自动续费 (余额扣,24h 前) · 国内支付 (易支付通道) · 国际支付 (Stripe+Creem) · 兑换码。
❌ 多产品计费中台 (需独立服务) · 退款系统 (计划中) · 发票/收据 (计划中)。

多产品接入推荐:认证用 Zitadel SSO;AI 计费用 lurus-api 共享钱包(按 tenant_id 统计);非 AI 产品各自处理支付共享 Zitadel user ID;未来独立 `lurus-billing` 中台。
