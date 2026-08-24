# Database Runbook

> **PostgreSQL 16.14** · in-cluster StatefulSet `lurus-pg` (pod `lurus-pg-0`) in ns **`database`** ·
> Service `lurus-pg-rw.database.svc.cluster.local:5432`(headless) · DB **`newhub`**,表在 **`public`** schema(40 张)。
> DSN 不写在文档里,真源 = Secret `lurus-newhub-secrets` 的 `SQL_DSN`。
>
> 2026-08-24 全文按 live 重核。旧版每一项都是 2026-04-23 退役的 `lurus-api`:
> 主机 `100.94.177.10:30543`、库 `lurusapi`、`kubectl exec -n lurus-system deploy/lurus-api`,
> 并教人手搭一条 crontab —— 而集群里**早已有**自动备份 CronJob(见下)。照旧版做备份/恢复
> 会打向一个不存在的库。

## Connection

```bash
# 直接进 PG(node 上)
kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c '\dt' | head

# 看应用实际用的 DSN(口令已脱敏)
kubectl get secret -n lurus-newhub lurus-newhub-secrets \
  -o jsonpath='{.data.SQL_DSN}' | base64 -d | sed -E 's#://[^@]*@#://***@#'
```

同一 PG 上还有 `identity`(platform,表在 `identity.*`/`billing.*` schema——`public` 只有 2 张表,
直接查 `public` 会误判「表不存在」)、`zitadel`、`lucrum`、`tally`、`newapi`。

App-level pool:`SQL_MAX_IDLE_CONNS`(100)、`SQL_MAX_OPEN_CONNS`(1000)、`SQL_MAX_LIFETIME`(60s)。
可选独立日志库:`LOG_SQL_DSN`(不设则日志与主库同库)。

## Backup

**已自动化,不要再手搭 crontab。** ns `database` 下两个 CronJob:

| CronJob | schedule | 实际时点 | 状态 |
|---------|----------|----------|------|
| `daily-pg-dump` | `0 2 * * *` | **02:00 Asia/Shanghai = 18:00 UTC** | ✅ 真在跑 |
| `weekly-s3-upload` | `0 3 * * 0` | 周日 03:00 CST | 🔴 **空转,见下** |

⚠️ 两个 CronJob 的 `.spec.timeZone` **都没设**,此时调度跟随 controller-manager 的时区,
而本节点 TZ 是 `Asia/Shanghai` ⇒ **不是 UTC**。核实法:`lastScheduleTime` 显示 `18:00:00Z`,
与 02:00 CST 吻合。

`daily-pg-dump` 转储**整个实例的每个库 + globals**(角色/口令;只有 per-DB dump 无法重建
`lurus`/`zitadel` 角色——2026-07-17 演练暴露的 identity-only 缺口),落 PVC `lurus-pg-backup`
的 `/backups`,保留 30 天。

```bash
# 备份是否真的发生了 —— 只看 Job 状态不够,必须看日志
kubectl get cronjob -n database
P=$(kubectl get pods -n database --no-headers | grep daily-pg-dump | tail -1 | awk '{print $1}')
kubectl logs -n database "$P" | tail -20     # 应看到每个库的 "pg_dump <db> complete: <size>"
```

### 异地副本:**在做,但不在 k8s 里** —— 别被 `weekly-s3-upload` 骗了

⚠️ **先读这条再下任何「没有异地备份」的结论。** 异地这一环归**宿主层**,不归 k8s:

| 环节 | 位置 | 2026-08-25 实测 |
|------|------|-----------------|
| 异地推送 | 宿主 cron `/etc/cron.d/*offsite* → /usr/local/sbin/pg-backup-offsite.sh`(源码 `2l-svc-platform/deploy/r6-host/`) | `40 2 * * * `(CST)。stamp `08-25 02:40:09`、`exit_code=0 files_transferred=8`、日志 `Total transferred file size: 22,091,129 bytes` |
| 传输方式 | `rsync -a --delete-after` → 另一台主机 | 专用密钥被 `rrsync` **强制命令**锁死,只能跑 rsync(试图执行别的命令会被拒) |
| 恢复演练 | 宿主 cron `32 5 * * 0` → `scripts/dr-drill.sh` | **每周**把最新一批备份全库恢复进一次性 ns、拉起真 platform-core 冒烟对账并实测 RTO;last-success `08-23 05:32` |
| 新鲜度告警 | `backup-freshness.sh` → textfile collector → netdata | `lurus_backup_offsite_age_seconds` / `_exit_code` 在流(实测 age=6173s),告警 `backup_offsite_stale` / `_leg_failing` |

🔴 **k8s 的 `weekly-s3-upload` 是个诱饵,不是异地机制。** 它每次报 `Complete 1/1`,日志却是
`S3 upload disabled (BACKUP_S3_ENABLED!=true) — exiting 0` —— 第一行就是 env 闸,未开即
exit 0。它是**规划中的第二条异地腿(S3)**,至今未启用;真正在工作的是上面那条 rsync 腿。

> **两个教训,后一个更贵:**
> ① 带 `if flag != true; exit 0` 前置闸的定时任务,判定其是否生效**只能读日志或产物**,
> 不能读 Job/Pod 状态。
> ② **只查了 k8s 一层就断言「异地备份从未发生」是错的**(2026-08-24 我就是这么误报的)。
> 本平台的备份/巡检/演练大量住在**宿主 cron**(`deploy/r6-host/` 下有 dr-drill、
> backup-freshness、cert-expiry、k8s-drift、money-path 等十余条)。**否定式结论必须把
> 宿主层也查过**:`ls /etc/cron.d/ | grep lurus` + 对应的 stamp/status 文件。

## Restore

```bash
# 列出可用产物
kubectl exec -n database lurus-pg-0 -- ls -lh /backups 2>/dev/null || \
  kubectl run -n database --rm -it restore-shell --image=postgres:16-alpine --restart=Never \
    --overrides='{"spec":{"volumes":[{"name":"b","persistentVolumeClaim":{"claimName":"lurus-pg-backup"}}],"containers":[{"name":"restore-shell","image":"postgres:16-alpine","stdin":true,"tty":true,"volumeMounts":[{"name":"b","mountPath":"/backups"}]}]}}' -- sh

# 全库 / 单表恢复(在挂了 /backups 的 pod 内)
pg_restore -U postgres -h lurus-pg-rw.database.svc -d newhub --clean --if-exists /backups/lurus-newhub-<TS>.dump
pg_restore -U postgres -h lurus-pg-rw.database.svc -d newhub --table=users --clean --if-exists /backups/lurus-newhub-<TS>.dump
```

PITR(WAL 归档)见 `doc/runbook/pg-restore.md`(wal-g)。

### 演练:两条,别混淆

- **全平台**:宿主 cron `32 5 * * 0` 跑 `2l-svc-platform/scripts/dr-drill.sh` —— 全库恢复进
  一次性 ns + 拉起真 platform-core 冒烟对账 + 实测 RTO。**这是权威演练**。
- ⚠️ 本仓的 `scripts/pg-restore-drill.sh` 测的是 **wal-g/S3**,不是现役的 pg_dump 链路,
  且 `WALG_S3_PREFIX` 未设时**静默 skip**。它跑绿**不能**说明现役备份可恢复。

### 单库快速验证(非破坏性,10 秒,可随时手跑)

```bash
D=/backups/lurus-newhub-<TS>.dump   # 宿主路径见 PVC lurus-pg-backup 的 .spec.local.path
kubectl exec -n database lurus-pg-0 -- psql -U postgres -q \
  -c 'DROP DATABASE IF EXISTS restore_drill_tmp;' -c 'CREATE DATABASE restore_drill_tmp;'
cat "$D" | kubectl exec -i -n database lurus-pg-0 -- \
  pg_restore -U postgres -d restore_drill_tmp --no-owner --no-privileges
# 逐表对账,再 DROP DATABASE restore_drill_tmp;
```

**2026-08-25 首次实跑结果(此前从未验证过现役 pg_dump 产物可恢复)**:
两侧均 **40 张表**;`users/tokens/channels/tenants/tenant_credit_pools/abilities/options/
schema_migrations/audit_events` **逐表相等**;唯一差异是 `logs`——按快照时刻
(`created_at < 1787594401`)切开后**两边都是 18589 行**,live 之后多出的 49 行在恢复库中为 0,
即纯粹是快照后的新写入。⇒ **产物真实可恢复,且与快照时刻逐表一致。**

> 「有 30 天的 dump」和「dump 能恢复」是两件事。上面这段的价值不在命令,在**对账那一步**——
> 一个恢复成功但内容为空的库,`pg_restore` 同样退出 0。

## Migration

两条独立机制,**都在每个 master-capable 副本上跑**,由两把 advisory lock 串行化
(`57e22c8a` 起与 leader lease 解耦,滚动更新不再漏跑):

1. GORM `AutoMigrate` —— 建缺失的表、加新列;**不删列、不改类型、不删表**。
2. `internal/pkg/migration` 的 embedded SQL runner —— `migrations/` 目录,`MIGRATIONS_AUTO_RUN=false` 可关。
   Baseline 契约:001–020 只记账永不执行;021 起必须 PG-only + 幂等,ID 先在 root
   `doc/coord/migration-ledger.md` 预留。

```bash
# 带 migration 的部署后核对
curl -s https://test-newhub.lurus.cn/api/health          # checks.schema_migrations
curl -s https://test-newhub.lurus.cn/metrics | grep schema_migrations   # pending 应为 0
kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \
  'select version, applied_at from schema_migrations order by version desc limit 5;'
```

手工变更(AutoMigrate 做不到的重命名/改类型/删除):

```sql
ALTER TABLE users RENAME COLUMN old_name TO new_name;
CREATE INDEX CONCURRENTLY idx_logs_tenant_id ON logs(tenant_id);   -- 大表必须 CONCURRENTLY
ALTER TABLE tokens ALTER COLUMN quota TYPE bigint;
```

变更前:先备份 · 先在 STAGE 验 · 查长事务(`SELECT * FROM pg_stat_activity WHERE state='active';`)。

## Monitoring

```sql
SELECT count(*) FROM pg_stat_activity WHERE datname='newhub';                               -- 连接数
SELECT relname, pg_size_pretty(pg_total_relation_size(relid)) FROM pg_catalog.pg_statio_user_tables
  ORDER BY pg_total_relation_size(relid) DESC LIMIT 20;                                     -- 表大小
SELECT query, calls, mean_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 20;
SELECT relname, n_dead_tup, last_vacuum, last_autovacuum FROM pg_stat_user_tables ORDER BY n_dead_tup DESC;
```

## Disaster Recovery

- **DB 不可达**:`kubectl get pods -n database` → `kubectl logs -n database lurus-pg-0` →
  查连接池耗尽(应用日志 "too many connections")。**重启应用重置连接池要走 ArgoCD**——
  `kubectl rollout restart` 会被 selfHeal 影响,应急路径见 `staging-deploy.md`。
- **数据损坏**:先停写(把 Deployment 副本数经 git 改为 0 并让 ArgoCD 收敛)→ 从 `/backups` 恢复 → 验证 → 恢复副本数。
- **Schema 漂移**:`kubectl logs -n lurus-newhub deploy/lurus-newhub | grep -i migration`,
  并对照 `/metrics` 的 `lurus_gateway_schema_migrations_pending`。
