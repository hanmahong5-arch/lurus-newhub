# 检索类计量(embeddings / rerank):单位、计价与排查

**Source**:消费日志新增的四列(migration 053)、`SearchUnitPrice` 选项、`lurus_retrieval_usage_total` / `lurus_relay_invalid_usage_total` 指标。
**Triggered by**:rerank 收费金额与厂商账单对不上;日志里 `usage_source` 出现大量 `estimated`;调用方收到 502 `invalid_provider_usage`;要按「接口类型 / 计费单位」看用量。

## 1. 日志里的四个字段

每条消费日志(`logs` 表,`internal/domain/entity/log.go`)带:

| 字段 | 含义 |
|---|---|
| `usage_unit` | 这次**按什么单位结算**:`token`(按 token 倍率)或 `search_unit`(按检索单位,仅 rerank 且该模型配了 `SearchUnitPrice`)。空串 = migration 053 之前写入的老行(token 语义),rankings 里归入 `(unrecorded)` 桶,不会被悄悄改标成 `token` |
| `usage_quantity` | 该单位下的数量:`token` 时是 `prompt+completion` token 数,`search_unit` 时是检索单位数 |
| `usage_source` | 用量数字从哪来:`upstream` = 厂商回报;`estimated` = 网关自己估算(厂商没回 usage,或响应里没有 usage 对象);`unreported` = 厂商没回且没有可结算的数量(此时按现有规则不扣费并记错误日志)。空串 = 老行 |
| `retrieval_documents` | rerank = 本次打分的文档数;embeddings = 本次嵌入的 input 条数;其他接口为 0 |

写入点:`internal/app/relay/compatible_handler.go`(`postConsumeQuota`)。同一份四元组也随 NATS usage 事件和沉淀事件下发(`internal/pkg/nats/usage_recorded.go`)。价格目录接口(`GET /api/v2/{tenant}/pricing`)对每个模型多回 `usage_unit` 与 `search_unit_price`(未配置时不出现该字段)。

## 2. 计价公式

只有 **rerank 且该模型配置了 `SearchUnitPrice`** 才走检索单位计价,其余一律走原有 token / 按次路径:

```
units = ceil(documents / 100) × queries        // Cohere 口径;queries 在网关恒为 1
quota = SearchUnitPrice[model] × units × 分组倍率 × QuotaPerUnit
```

- `documents` 取自**请求体里的文档数**(调用方可控、厂商无法虚报),不取厂商回报。0 个文档 = 0 单位。
- 预扣费与结算用同一个金额(`helper.ModelPriceHelper` 与 `postConsumeQuota` 各算一次同一公式),与厂商回报的 token 数无关。
- 例:模型价 `0.002`,250 个文档 → `ceil(250/100)=3` 单位 → 基础金额 `0.006`(再乘分组倍率与额度换算)。
- 代码:`internal/pkg/setting/ratio_setting/search_unit_price.go`(`SearchUnits`、常量 `SearchUnitDocsPerUnit=100`)。

## 3. 配置 SearchUnitPrice

选项键 `SearchUnitPrice`,值是 JSON 对象,键为模型名,值为每检索单位的价格(与 `ModelPrice` 同一类美元口径):

```bash
curl -X PUT https://hub.lurus.cn/api/v2/admin/options \
  -H "Authorization: Bearer <root access token>" -H "Content-Type: application/json" \
  -d '{"key":"SearchUnitPrice","value":"{\"model-a\":0.002}"}'
```

- 整表覆盖:PUT 会替换整张表,先 `GET /api/v2/admin/options` 读出现有值再合并。
- 校验:不是合法 JSON 对象、价格为负或非有限数时整个请求被拒,**线上的旧价格表不会被清掉**。
- 修改后立即生效并使价格目录缓存失效。
- 仅 root 可写(`requireRoot`)。

### 「未配置」不等于「免费」

| 状态 | 键 | 行为 |
|---|---|---|
| 未配置 | 表里**没有**该模型 | 回落 token 计价;若 token 倍率/价格也没配,走现有「ratio or price not set」报错——**未知价格永远不会静默变成免费** |
| 显式 0 | 表里有该模型且值为 `0` | 按检索单位计价,金额为 0,即**有意免费**;日志 `usage_unit=search_unit`、数量照记 |

要让 rerank 免费必须写 `0`,不要靠删键。

## 4. 厂商用量自相矛盾:502 `invalid_provider_usage`

rerank 响应里的 usage 若自相矛盾(例如 `total_tokens=0` 而 `prompt_tokens>0`,或出现负数),网关返回 **502 `invalid_provider_usage`**,该次**不扣费**,并给 `lurus_relay_invalid_usage_total{relay_mode="rerank"}` 加一。只有一个计数字段(如 Jina 只回 `total_tokens`)不算矛盾,按该字段结算。厂商完全不回 usage 时用网关估算值并标 `usage_source=estimated`。

另一个同为 502 的错误是 `invalid_provider_response`(响应体格式合法但内容不可信,如 rerank 结果排序/索引不一致),不重试、不切渠道。两者的契约见 `docs/openapi/relay.json` 的 `/v1/rerank`。

某个厂商开始批量触发 `invalid_provider_usage` 时,表象是收入悄悄下降而不是报错飙升;该指标目前**没有** Netdata 告警(见 `internal/pkg/metrics/retrieval.go` 注释),需要人工看。

## 5. 在 rankings 里按接口类型 / 计费单位看用量

排行接口新增两个分组维度(`by`):

- `by=relay_mode`:按接口类型分组,标签由 `relayconstant.RelayModeLabel` 生成(未列入的归 `other`);
- `by=usage_unit`:按计费单位分组(`token` / `search_unit` / `(unrecorded)`)。

```bash
# 租户管理员 / 部门负责人
curl "https://hub.lurus.cn/api/v2/~/analytics/rankings?by=relay_mode&hours=168" -H "Authorization: Bearer <access token>"
# 平台管理员(全租户)
curl "https://hub.lurus.cn/api/v2/admin/analytics/rankings?by=usage_unit&hours=168" -H "Authorization: Bearer <access token>"
```

控制台:Dashboard 的「Usage breakdown」面板有分组选择器,含「按接口类型」与「按计费单位」。`hours` 取 1–720 并就近吸附到缓存预设。注意 `total_tokens` 列对 `search_unit` 行仍是 token 数(rerank 往往很小或为估算值);看检索单位数量请直接查日志的 `usage_quantity`。

## 6. Prometheus 指标

| 指标 | 标签 | 含义 |
|---|---|---|
| `lurus_retrieval_usage_total` | `unit`, `source` | 已结算的 rerank / embeddings 调用数,按计费单位与用量来源分;`source="estimated"` 的占比 = 非厂商回报计量的比例 |
| `lurus_relay_invalid_usage_total` | `relay_mode` | 因 usage 自相矛盾被 502 拒绝的次数 |

两者都由宿主 Netdata 的 `prometheus` collector 从 `/metrics` 抓取,服务侧无需改动。

## 7. 排查清单

1. rerank 金额不对:先查价格目录该模型的 `usage_unit`。是 `token` 说明没配 `SearchUnitPrice`(或模型名对不上);再核日志 `retrieval_documents` 与 `usage_quantity` 是否满足 `ceil(docs/100)`。
2. `estimated` 偏多:`lurus_retrieval_usage_total{source="estimated"}` 按渠道排查,多半是某厂商不回 usage。
3. 调用方报 502:看响应 `code` 是 `invalid_provider_usage` 还是 `invalid_provider_response`,再看对应渠道的厂商回包。
