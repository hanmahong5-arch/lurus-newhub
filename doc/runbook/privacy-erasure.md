# Privacy Erasure Runbook (PIPL §47 账号擦除级联)

> 平台侧冷静期结束后调用 newhub 的内部端点,newhub 用一个 leader-only 后台任务
> 按步骤处置该用户的数据。
> 代码:`internal/adapter/handler/internal_privacy_erase.go`(入口) ·
> `internal/lifecycle/privacy_erasure.go`(执行器) ·
> `internal/adapter/repo/privacy_erasure.go`(每一步的 SQL 原语)。
> 台账表:`privacy_erasure_requests`(migration 020),**代码里没有任何删除这张表的路径**——
> 行本身就是合规证据。
>
> Last review: 2026-09-20(cycle-13 L5:新增 content 步骤 + `download_logs` 写入最小化)。

## Source / Triggered by

| | |
|---|---|
| Source | platform 调 `POST /internal/v1/privacy/erase`(header `X-API-Key: lurus_ik_…`,scope `user:delete`) |
| Triggered by | 人工排障:`privacy_erasure_requests` 里有 `status='pending'` 且 `last_error <> ''` 的行,或某行 pending 超过一个擦除周期仍不前进 |
| Severity | 合规事项(不是页面告警);拖过监管答复窗口才是事故 |

查询单条请求状态:`GET /internal/v1/privacy/erase/:event_id`(scope `user:read`)。

## 执行器

- 任务名 `privacy-erasure`,**leader-only**,出现在 `GET /api/v2/admin/system/tasks`。
- 周期默认 60 秒(`PRIVACY_ERASURE_INTERVAL_SECONDS` 覆盖,单位秒)。
- 每次 pass 取最多 10 条 pending,按 `created_at` 升序;单条失败**只**写进那一行的
  `last_error`,不阻塞其它行,下一 tick 从持久化游标续跑。
- 整个 pass 无错才推进心跳 `lurus_gateway_leader_task_last_success_timestamp_seconds{task="privacy-erasure"}`。
- 批次大小 500 行(`erasureBatchSize`)。

## 六个步骤(`privacy_erasure_requests.step` = **最后一个已完成**的步骤)

| `step` 值 | 这一步做了什么 |
|---|---|
| `''`(未开始) | — |
| `tokens_deleted` | `tokens` 硬删(含软删行)· `user_totps` / `user_totp_backup_codes` · `user_sessions` · `response_registry` 硬删 · `admin_permission_grants` 撤销 |
| `mappings_deleted` | `user_identity_mapping` 硬删(邮箱 / 显示名 / preferred_username) |
| `content_deleted`(cycle-13 L5) | `chat_messages` → `chat_sessions` 批量硬删 · `midjourneys` 先终态化再批量硬删 · 在途 `tasks` 终态化后抹 `properties/data/fail_reason` · `quota_data.username` → `[erased]`(含本副本的写回缓存)· `playground_presets` 硬删 |
| `logs_anonymized` | `logs` 批量假名化(`username`/`token_name`/`ip`/`content`/`other`),计费列保留;每批顺带尽力清 Meilisearch |
| `audit_scrubbed` | `audit_events` 抹 `ip`/`details`,动作与时间戳保留(走 migration 024 的 `app.audit_redaction` 事务级 GUC) |
| → `status='completed'` | `users` 行就地匿名化 + 软删,`lurus_account_id` 置 NULL;写审计事件 `privacy.erasure.completed` |

**保留的东西是有意的**:`logs`/`quota_data`/`tasks` 的计费列、`redemptions`、池扣款行、
以及本表自己,都在 PIPL 的法定留存豁免下保留;这是假名化不是删除。
不在覆盖范围内的(入口 handler 的注释里也写着):PG 备份与 WAL、Meilisearch 快照、
已经发出去的 stdout 日志、上游供应商自己的日志。

本版本遇到**自己不认识的游标值**(下一轮加了新步骤、随后回滚到本版本)时,
`executeErasure` 返回错误并写进 `last_error`,**不会**把请求标成完成;
cycle-13 之前没有这条兜底,不认识的游标=静默空转:请求永远 pending,
而 leader 心跳照常推进,没人看得见。
反过来的一半修不了:回滚到 **cycle-13 之前**的镜像,那个二进制里没有这条兜底,
停在 `content_deleted` 的行在它眼里仍然是静默空转——回滚窗口里要盯
`privacy_erasure_requests` 的 `updated_at` 是否在动。

## download_logs:为什么它不在级联里

`download_logs` 没有 `user_id`/`tenant_id` 列,下载者是匿名的,**按用户的级联没有键可以选中它**。
替代措施是写入时最小化(`internal/app/release_service.go` `HandleDownload`,cycle-13 L5):

- `ip_address` 存 `repo.MaskIP` 的粗化结果(IPv4 /24、IPv6 /48);解析不了的地址存
  `0.0.0.0`(该列是 `inet`,空串会让这条 INSERT 直接失败,而它跑在一个没人读错误的
  goroutine 里)。
- `user_agent` / `referer` 截到 512 字节,按 UTF-8 边界截(不会切在多字节字符中间)。
- 时间维度的清理在 `DOWNLOAD_LOG_RETENTION_DAYS`(默认 90 天,开着)——
  见 [log-retention](log-retention.md)。

这条豁免在 `internal/adapter/repo/erasure_model_coverage_test.go` 的豁免表里有对应条目;
那个结构闸会强制"AutoMigrate 注册的、带个人数据形状字段的模型,要么被级联覆盖,
要么在带理由的豁免表里"。

## 已知残留(写在这里就是为了不被当成已经解决)

1. **MJ 轮询器的在途窗口**:`repo.MjUpdate` 是 `DB.Save`,GORM 对 0 行的 UPDATE 会回退成
   Create。级联删除前会先把该用户的 `midjourneys` 行终态化(`progress='100%'`),
   于是**之后**的轮询不再选中它们;但一次已经把行读进内存的轮询 pass 仍会在删除后把行
   连同 prompt 写回来。实测钉在 `TestPrivacyErasure_PG_MidjourneyPollerResidue`。
   彻底修需要改 `MjUpdate` 不再回退 Create(relay/轮询器的文件)。
2. **跨副本的 quota_data 缓冲**:`quota_data` 走进程内写回缓存,级联是 leader-only。
   擦除时先清本副本的缓存;其它副本在 flush 的 create 分支查一次"这个用户的擦除是否
   已经过了 content 步骤",是则写 `[erased]` 而不是明文用户名
   (`writeQuotaDataSnapshot`)。**没覆盖的**:请求还没走到 content 步骤时的 flush——
   那时表里本来就是明文,不算回写。
3. **本轮之前完成的擦除没有跑过 content 步骤**:见下面的 owner 事项。
4. **在途任务终态化绕过了退款路径**:content 步骤先把该用户还在轮询中的 `tasks` 行改成
   FAILURE/100%(`TerminaliseOpenTasksForUser`),轮询器因此不再选中它们——但退款是
   轮询器的 FAILURE 臂(`handler.refundTaskQuota`)做的,已经是 FAILURE 的行永远进不了
   那条臂。结果:该任务的 **key 额度与租户池仍然是扣掉的**(用户余额那条腿无所谓,
   账号正在被擦除;池是经销商付的钱,不无所谓)。同一 UPDATE 提交前已把行读进内存的
   一次轮询 pass 仍会把 `data`/`fail_reason` 写回一次(与 MJ 的在途窗口同类,
   步骤从游标重跑会再抹一次)。见下面的 O-erasure-inflight-refund。

## O-erasure-backfill(owner 事项,一次性)

`content_deleted` 是 cycle-13 才加的步骤。**在此之前标成 `completed` 的请求,
它们的 chat/midjourney/tasks/quota_data 内容从来没有被处置过**;
执行器只列 `status='pending'` 的行,所以不会自己回头补。

重放配方(在 R6 的 PG 上,先在 UAT 走一遍):

```sql
-- 0) 先留档:重放会覆盖 completed_at,原值是合规证据
\copy (SELECT id, event_id, account_id, user_id, status, step, completed_at
       FROM privacy_erasure_requests WHERE status = 'completed')
  TO '/tmp/erasure_completed_before_backfill.csv' CSV HEADER

-- 1) 有多少行要重放(<cutoff> = cycle-13 镜像上线的时间戳)
SELECT count(*) FROM privacy_erasure_requests
 WHERE status = 'completed' AND completed_at < '<cutoff>';

-- 2) 把游标退回 mappings_deleted 并重新挂 pending
UPDATE privacy_erasure_requests
   SET step = 'mappings_deleted', status = 'pending',
       completed_at = NULL, last_error = '', updated_at = now()
 WHERE status = 'completed' AND completed_at < '<cutoff>';
```

退回到 `mappings_deleted` 而不是 `''`:前两步(tokens/mappings)早就做完了,重跑它们
只是白扫;从 `mappings_deleted` 起跑会依次执行 content → logs → audit → users,
每一步本身幂等(logs/audit 按 `[erased]` 游标跳过已处理行,`AnonymizeUserRow` 重复写同样的值)。

重放的副作用,签字前要知道:
- `completed_at` 会被刷新成重放完成的时间(所以第 0 步先导出);
  `users.deleted_at` 同样会被 `AnonymizeUserRow` 重写成重放时间。
- 每条重放会再写一条 `privacy.erasure.completed` 审计事件。
- **4-eyes**:这是一条批量 UPDATE 合规台账的语句,要第二个人复核 `<cutoff>` 与行数。

## O-erasure-inflight-refund(owner 事项)

擦除时被终态化的在途任务(上面残留第 4 条)没有走退款。要么在 content 步骤终态化**之前**
对这些行调用与轮询器 FAILURE 臂相同的退款原语(`handler.refundTaskQuota`,它在 handler
包,repo 层够不到——需要把退款原语下沉或让擦除执行器经 handler 调),要么接受"擦除请求
里的在途任务不退款"并写进对客说明。查有没有这种行:

```sql
SELECT id, task_id, quota, submit_time FROM tasks
 WHERE user_id = :uid AND status = 'FAILURE' AND fail_reason = '[erased]';
```

## Verify(重放后 / 新请求完成后)

```sql
-- 该用户的内容面应为 0 行
SELECT (SELECT count(*) FROM chat_sessions      WHERE user_id = :uid) AS chat_sessions,
       (SELECT count(*) FROM chat_messages      WHERE user_id = :uid) AS chat_messages,
       (SELECT count(*) FROM midjourneys        WHERE user_id = :uid) AS midjourneys,
       (SELECT count(*) FROM playground_presets WHERE user_id = :uid) AS presets;

-- 保留但已抹干净的行
SELECT status, fail_reason, properties, data FROM tasks WHERE user_id = :uid;
SELECT username, quota, count, token_used     FROM quota_data WHERE user_id = :uid;  -- username = '[erased]'
SELECT count(*) FROM logs WHERE user_id = :uid AND username <> '[erased]';           -- 0

-- 台账
SELECT event_id, status, step, logs_scrubbed, last_error, completed_at
  FROM privacy_erasure_requests WHERE user_id = :uid;
```

UAT 上的等价探针(计划里 L5 的验收项):对一个 scratch 用户发起擦除,完成后
`chat_sessions` / `chat_messages` 该用户 0 行。

## Prevent

- 新增一张带个人数据的表时,`TestErasureCascadeCoversEveryPersonalDataShapedModel`
  会在它没被分类时直接红 —— 处置它或给出带理由的豁免,两者选一,不存在第三种。
- 新增一个 `step` 值时,回滚到**本版本**的镜像会把它当作未知步骤报错(而不是静默空转):
  回滚窗口里那几条请求停在原地、带着 `last_error`,窗口结束后自动续跑。
