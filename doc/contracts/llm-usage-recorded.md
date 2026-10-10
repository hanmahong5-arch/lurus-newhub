# 契约草案：`llm.usage.recorded`（用量沉淀事件）

> 状态：草案（v=1），发布方 = newhub，消费方适配器**不在 newhub 内**。
> 代码真源：`internal/pkg/nats/usage_recorded.go`（载荷）、`internal/app/sedimentation/usage_event.go`（发布）。
> 同意位与正文归档：migration 052，`internal/adapter/repo/log_body.go`。

## 1. 用途

租户同意"数据沉淀"后，每条已落库的计费日志产出一条**仅含元数据**的事件，供下游（记忆/分析系统）按 `request_id` 回拉正文或只做用量统计。事件**绝不携带 prompt/response 正文**。

## 2. 传输

- 总线：NATS JetStream，沿用现有 LLM 事件流（`LLM_QUOTA_NATS_STREAM`，默认 `LLM_EVENTS`），subject = `llm.usage.recorded`。
- 开关：与现有 NATS 发布器同一个（`LLM_QUOTA_NATS_ENABLED`）。关闭时不注册钩子、不起 worker，零行为。
- 外层信封（与其他 `llm.*` 事件一致）：

```json
{
  "event_id": "<uuid>",
  "event_type": "llm.usage.recorded",
  "account_id": 123,
  "payload": { ... },
  "occurred_at": "2026-10-09T10:00:00Z"
}
```

`account_id` 为产生该请求的用户 id（== platform account_id）。`event_id` 每次发布新生成，**不是**幂等键；幂等键用 `payload.request_id`（见 §5）。

## 3. 载荷（`payload`，v=1）

| 字段 | 类型 | 说明 |
|---|---|---|
| `v` | int | 契约版本，恒为 `1`；破坏性变更才递增 |
| `tenant_id` | string | 租户 id |
| `project_id` | int | 成本归集项目；`0` = 未归属 |
| `token_id` | int | 调用所用令牌 id |
| `end_user_hash` | string，可省略 | 调用方终端用户标识的租户内 HMAC，**非原值**；未提供则省略 |
| `model` | string | 请求的模型名 |
| `prompt_tokens` | int | |
| `completion_tokens` | int | |
| `quota` | int | 内部配额单位 |
| `charged_cny4` | int64 | 钱包实扣金额，单位 1/10000 元；`0` = 未记录（信用池/本地配额/旧行），不等于免费 |
| `request_id` | string | 关联键，对应日志 `other.request_id`，用于回拉正文与去重 |
| `created_at` | int64 | 日志行时间，unix 秒 |
| `has_body` | bool | 发布时是否已存在未过期的归档正文 |
| `relay_mode` | string，可省略 | 请求类型：`chat` `completions` `embeddings` `moderations` `images` `audio` `rerank` `systemone` `responses` `realtime` `gemini` `other`；未知（0）则省略。名称只增不改 |
| `usage_unit` | string，可省略 | 计量单位：`token` / `search_unit` / `request`；迁移 053 之前的旧行省略 |
| `usage_quantity` | int64，可省略 | 按 `usage_unit` 实际计费的数量（token 数、search unit 数……）；`0` 省略 |
| `usage_source` | string，可省略 | 用量来源：`upstream`（上游自报）/ `estimated`（网关估算）/ `unreported`（上游未报且无法估算）；旧行省略 |
| `retrieval_documents` | int，可省略 | rerank = 参与打分的文档数；embeddings = 输入条数（取自请求，不取自响应）；其余为 `0` 省略 |

**v 保持 1。** 载荷是**标量集合**：可以追加**可选标量字段**（`omitempty`，缺省时事件与旧版逐字节一致），**消费者必须忽略未知字段**；任何自由文本字段、字段改名或语义变化仍是破坏性变更，须递增 `v`。每次追加都要改本文件与 `usage_event_test.go` 里的允许键白名单（那是"有意识的契约变更"的闸门）。

search unit 口径（Cohere）：`units = ceil(documents / 100) × queries`，网关内 `queries` 恒为 1。

只对**消费日志**（计费成功的请求）产出事件；错误日志、无 `request_id` 的行不产出。

## 4. 发布条件与投递语义

发布当且仅当：租户 `sedimentation_consent = true`，且该行是带 `request_id` 的消费日志。**不要求**租户留存为 `full`：同意位控制"是否产出事件"，是否有正文由 `has_body` 表达。

- **at-most-once**：先落库后发布；队列有界（4096），队列满、broker 故障或进程重启会丢事件，不重试、不补发。丢失计入 `lurus_nats_publish_failed_total{subject_group="usage"}`。下游若需完整对账，应以 `GET /api/v2/admin/logs/export?format=jsonl`（游标分页）为准，事件只是增量提示。
- 发布失败**不影响**计费与日志主路径。
- `has_body` 在日志落库约 2 秒后探测（正文归档是异步写入），所以偶发"正文随后才落库"会得到 `false`；`has_body=false` 不代表之后一定没有正文，下游回拉 404 属正常。
- 撤回同意后，最多约 15 秒（同意缓存 TTL）内仍可能有事件发出，之后停止。
- 撤回同意时网关**同步删除**该租户已归档的全部正文（`PUT …/data-policy/sedimentation` 返回 `purged_bodies`；若中途失败返回 `purge_pending=true`，由每小时的清理任务补删）。撤回后下游按 `request_id` 回拉正文一律 404，下游若已复制正文，删除责任在下游。

## 5. 去重

下游以 `(tenant_id, request_id)` 去重。`request_id` 是网关为每个入站 HTTP 请求生成的 id（`middleware.RequestId`），渠道重试发生在同一个入站请求内，失败的尝试记为错误日志、**不发事件**，所以正常情况下一个 `request_id` 只对应一条消费日志、一条事件；仍要求幂等消费，因为 at-most-once 投递不排除进程重启后的重复落库场景。

## 6. 回拉正文

事件不含正文。需要正文时：

- 租户侧：`GET /api/v2/{tenant_slug}/logs/{request_id}/body`（租户管理员凭证）
- 平台侧：`GET /api/v2/admin/logs/{request_id}/body`（平台管理员凭证）
- 批量：导出接口 `format=jsonl&include_body=true`，每个附带的正文都会写 `log_body.read` 审计。

不存在、已过期、他租户三种情况返回完全相同的 404 `LOG_BODY_NOT_FOUND`。正文是过滤后的版本（内容规则的脱敏/拒绝已作用），每字段上限 64KB，`truncated` 标明是否被截断。

## 7. 保留与删除传播

- 正文按 `LOG_BODY_RETENTION_DAYS`（默认 30）过期，leader 上的 `log-body-cleanup` 分批清理；读取时已过期行视为不存在。
- 租户撤回同意：**停止发布事件**（约 15 秒内生效）。
- **已知缺口（需 L2 侧补）**：当前撤回同意（`SetTenantSedimentationConsent(false)`）只改标志，**不删除已存正文**，也不停止写入以外的清理；已存正文只能等 TTL 到期。若要求"撤回即在下一轮清理删除"，清理任务需增加"所属租户已撤回同意则删除"的分支。在补上之前，对外承诺只能是"撤回后不再新增、存量最长 `LOG_BODY_RETENTION_DAYS` 天后消失"。
- 用户被删除时，正文随隐私擦除级联硬删（L2 已实现）。
- 已被下游消费并写入自身存储的数据，**不会**因 newhub 过期或撤回而自动删除；下游需自行订阅撤回/删除语义并处理，newhub 不替下游删数据。

## 8. 下游适配器约束

- 消费适配器（例如写入记忆系统）**不在 newhub 内**，由下游自行部署。
- 写入记忆系统时**不得自带 `tenant_id`**：租户归属由适配器所持凭证决定，不得信任也不得转写载荷里的 `tenant_id` 去覆盖凭证的租户；`tenant_id` 在载荷里只用于路由与审计比对。
- 适配器只应持有读正文所需的最小凭证，并对 `request_id` 回拉结果按本契约的 404 语义容错。

## 9. 变更流程

改本契约前先在 `doc/coord/contracts.md` 查消费者；改后同步 `contracts.md`、`service-status.md`、`changelog.md`。
