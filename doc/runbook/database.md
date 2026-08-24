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

### 🔴 异地副本目前不存在

`weekly-s3-upload` 每次都是 `Complete 1/1`,**但日志是**
`S3 upload disabled (BACKUP_S3_ENABLED!=true) — exiting 0`。它的第一行就是这个 env 闸,
未开时立刻 exit 0 —— 所以 Job 状态永远绿,而**从未上传过任何东西**(实测 2026-08-24:
最近三次 16d/9d/2d 全是这一行)。

⇒ 现状:备份**只在同一块盘上**,盘毁即全失。开启需要 `BACKUP_S3_ENABLED=true` +
`BACKUP_S3_BUCKET` + 对象存储凭证(owner-gated)。

> 这是「绿色 Job ≠ 工作发生了」的样本:任何带 `if flag != true; exit 0` 前置闸的定时任务,
> 判定其是否生效**只能读日志或产物**,不能读 Job/Pod 状态。

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
