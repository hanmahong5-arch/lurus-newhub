# Log Retention Runbook

> `logs` 与 `download_logs` 的按时间保留任务(cycle-13 L6)。
> 代码:`internal/lifecycle/log_retention.go` · `internal/adapter/repo/log_retention.go`
> 任务名 `log-retention`,**leader-only**,出现在 `GET /api/v2/admin/system/tasks`。
>
> **默认全关。** `LOG_RETENTION_DAYS` 与 `LOG_RETENTION_MONEY_DAYS` 都默认 0 = 不删任何行,
> 所以上线这个任务本身不改变一行数据。合同窗口值是 owner 事项(计划里的 **O-retention**)。

## 为什么有这个任务

在它之前 `logs` **没有任何保留路径**:唯一的删除是运维手动打的
`DELETE /api/log/`(`internal/adapter/handler/log.go` → `repo.DeleteOldLog`),同步执行、
没有排期;`download_logs` 连手动路径都没有。生产 manifest 里却写着这张表"经同一
DeleteOldLog 保留老化,体量有界"——那句话描述的东西从来没运行过。

2026-09-20 实测:生产 `logs` 29,607 行 / 20 MB。现在不是容量问题,设窗口前先想清楚
**删掉之后谁会来要**(见下面"和账单的关系")。

## 两个窗口,两条下限

行按 `logs.type` 分成两类,**互不重叠、合起来覆盖 entity 声明的每一种类型**
(`TestLogRetentionTypes_PartitionEveryDeclaredLogType` 解析 `entity/log.go` 守着这条)。

| 类别 | `type` | env | 下限 | 默认 |
|---|---|---|---|---|
| 钱行 | topup(1) / consume(2) / refund(6) | `LOG_RETENTION_MONEY_DAYS` | **400 天** | 0 = 关 |
| 诊断行 | unknown(0) / manage(3) / system(4) / error(5) | `LOG_RETENTION_DAYS` | **30 天** | 0 = 关 |

**低于下限 = 拒绝,不是截断。** 设 `LOG_RETENTION_DAYS=3` 的结果是一条 SysError
(`... is below the 30-day floor; refusing to delete anything on this window`)加上**零删除**,
不是"按 30 天删"。往上截断会保留比运维要求的更多、往下截断会删掉他们还需要的——两种都是
惊吓,所以两种都不做。

## 和账单的关系(设钱行窗口前必读)

`/api/v2/:tenant_slug/billing/invoices` 与 `/api/v2/:tenant_slug/billing/self`
是**直接对 `logs` 做 `SUM(quota)`** 的(`v2_billing_invoices.go`、`billing_self.go`),
没有独立的发票快照表。**删掉一条 consume 行就等于改写一张已经给客户看过的账单。**

400 天的下限就是从这里来的:12 个自然月 + 当月,十三张月账单仍然可以重新生成。
真要缩短,先回答"缩短之后谁来保证历史账单可复现"。

refund 行和 consume 行同类,不是疏忽:refund 是 consume 的反向分录,只老化其中一边会让
某个时间段的日志合计**高于**实际扣款。

## download_logs

`DOWNLOAD_LOG_RETENTION_DAYS`,**默认 90 天(开着)**。这张表按下载事件存
`ip_address` / `user_agent` / `referer`,而下载者是匿名的——没有 `user_id`,擦除请求
够不到它,时间是唯一的杠杆。0 = 关。

## 其它旋钮

| env | 默认 | 作用 |
|---|---|---|
| `LOG_RETENTION_BATCH` | 500 | 每条 DELETE 的行数上限 |
| `LOG_RETENTION_MAX_BATCHES_PER_PASS` | 200 | 一次 pass 的批次上限;剩下的积压留给后续 pass |
| `LOG_RETENTION_INTERVAL_SECONDS` | 86400 | tick 周期 |

删除用 `id IN (SELECT id … ORDER BY id LIMIT ?)` 子查询分批——gorm 两种方言的 DeleteClauses
都不渲染 LIMIT,裸 `.Limit(n).Delete()` 会退化成一条无界 DELETE。默认上限
500 × 200 = 100,000 行/pass:第一次在大积压上开窗口会连着跑几天才追平,这是有意的,
不要为了"一次删完"把上限调到无穷。

## 怎么开

1. 决定两个窗口(owner)。
2. 改两份 manifest 的 env(`deploy/k8s/r6-stage/deployment.yaml`、`deploy/k8s/r6-uat/deployment.yaml`),
   **先 UAT**。走 git → ArgoCD,不要 `kubectl set env`(selfHeal 会回滚)。
3. 第一次开之前先量积压:

```sql
-- 诊断行里会被 30 天窗口删掉的行数
SELECT count(*) FROM logs WHERE type IN (0,3,4,5) AND created_at < extract(epoch from now() - interval '30 days');
-- 钱行里会被 400 天窗口删掉的行数
SELECT count(*) FROM logs WHERE type IN (1,2,6) AND created_at < extract(epoch from now() - interval '400 days');
```

4. 部署后核实任务真的在跑:

```bash
curl -s https://hub.lurus.cn/api/v2/admin/system/tasks   # 需要 admin 会话;应含 log-retention / leader_only=true
kubectl logs -n lurus-newhub deploy/lurus-newhub | grep 'log retention'
```

## 怎么看它有没有落后

积压 gauge 报的是"窗口外还剩多少行",不是"这次删了多少":一个持续不降的积压
= 批次预算追不上写入速度。指标名与告警在 `deploy/r6-host-netdata/`:

- `lurus_gateway_log_retention_deleted_total{table}`
- `lurus_gateway_log_retention_pending_rows{table}`

> 状态:两个序列由任务在每轮 pass 里写(`metrics.RecordLogRetention` /
> `SetLogRetentionPending`,`internal/lifecycle/log_retention.go`;任务由
> `cmd/server/main.go` 启动)。窗口为 0 的那条腿不跑,它的 `table` 标签就不存在——
> 那是「没开」,不是「坏了」。任务同时把同样的数字写成 SysLog 行
> (`log retention: deleted N row(s) from <table>` /
> `log retention: <table> still has N row(s) past the window`),两边可以互核。

## 039 索引失效(INVALID)怎么查

删除靠 `idx_logs_tenant_created_id`(migration 039,`CREATE INDEX CONCURRENTLY`)走
`(tenant_id, created_at)` 范围。CIC 中途失败会留下一个 planner 不用的 INVALID 索引,
而 `IF NOT EXISTS` 下次启动会跳过它——表现为保留任务每批都慢、积压 gauge 不降。先查:

```sql
SELECT indexrelid::regclass AS invalid_index, indrelid::regclass AS on_table
  FROM pg_index WHERE NOT indisvalid;
```

有结果就按 [database.md](database.md) "no-transaction migration" 一节的修复步骤
(`DROP INDEX CONCURRENTLY` 后手工重建)。设了 `LOG_SQL_DSN` 的部署要在**日志库**上查。

## 回滚

把两个 env 设回 0(或删掉)并让 ArgoCD 收敛。**已删除的行不会回来**——
回滚只停止继续删。真要恢复得从 `/backups` 的 dump 里捞(见 `database.md`)。
