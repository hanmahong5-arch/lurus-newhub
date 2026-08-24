# High Availability Deployment Guide

> 决策依据见 ADR `doc/decisions/ha-deployment.md`。唯一 manifest:`deploy/k8s/r6-stage/`。
>
> 2026-08-24 按 live 重核。旧版通篇是 2026-04-23 退役的 `lurus-api`/ns `lurus-system`,
> 且有三处与现状相反:它描述了一个**集群里并不存在的 PodDisruptionBudget**、
> 把 readiness 写成 `/api/status`(实际是深检 `/api/health`)、
> 并推荐 `kubectl scale` / `rollout undo`(**会被 ArgoCD selfHeal 回滚**)。

`lurus-newhub` 的 HA:宿主 nginx → NodePort 30850 → 3 副本 → 共享 PostgreSQL + Redis。

**前提**:Redis 可达(`REDIS_CONN_STRING`)、PostgreSQL 主库、**所有副本 `SESSION_SECRET` 相同**
(同一个 Secret 注入,天然一致)。

## 现状(live)

| 项 | 值 |
|----|-----|
| replicas | **3**(leader election 演练:杀掉 leader,看备用在 lease TTL 内接管) |
| strategy | RollingUpdate,`maxUnavailable: 0` / `maxSurge: 1` |
| PodDisruptionBudget | **无**(单节点集群,PDB 挡不住节点级中断;不要照旧版去建一个) |
| podAntiAffinity | **无**(单节点,反亲和会让副本永远 Pending) |
| liveness | `GET /api/status`,浅检,`initialDelaySeconds:30 periodSeconds:15` |
| readiness | `GET /api/health`,**深检**(DB + breaker),`initialDelaySeconds:10 periodSeconds:5 failureThreshold:3 timeoutSeconds:2` |
| preStop | `sleep 5`,给在途 relay 请求排空 |

浅 liveness / 深 readiness 的分工与其相关风险(3 副本共享一个 PG ⇒ PG 抖动会同时把 3 个副本
踢出 Service)在 `deploy/k8s/r6-stage/deployment.yaml` 的探针注释里有完整论证,改探针前先读。

## 状态归属

| 组件 | 存储 | 多副本行为 |
|------|------|-----------|
| Session / 限流 | Redis(`lurus-system` ns,DB **2**) | 副本间共享 |
| 渠道缓存 | PostgreSQL → 内存 | 各副本独立同步(`SYNC_FREQUENCY=60` **秒**) |
| JWKS 缓存 | IdP 端点 | 各副本独立刷新 |
| 倍率表 | PostgreSQL | 各副本独立加载 |
| master-only 后台任务 | DB lease | 仅 leader 执行 |
| DB migration | 两把 advisory lock | **每个 master-capable 副本都跑**,由锁串行化(`57e22c8a` 起与 lease 解耦) |

## 运维操作

副本数、镜像、探针**都改 git 源 manifest**,由 ArgoCD 收敛:

```bash
# 扩缩容 = 改 deploy/k8s/r6-stage/deployment.yaml 的 replicas 并 merge 到 main
# 回滚   = revert 那次 auto-pin commit(不是 kubectl rollout undo)
# 详见 doc/runbook/staging-deploy.md

# 只读核验
ssh root@100.122.83.20 "kubectl get deploy,pods -n lurus-newhub -o wide"
ssh root@100.122.83.20 "kubectl get app lurus-newhub -n argocd"    # 应为 Synced / Healthy
curl -s https://test-newhub.lurus.cn/api/health                     # 四检全 ok
```

🔴 `kubectl scale` / `kubectl rollout restart` / `kubectl set image` 在本服务上**无效**:
ArgoCD `automated + selfHeal` 会把它们回滚,只会制造「改了没生效」的假象。

## 告警阈值

`deploy/grafana/newhub-alerts.yaml` 与 `deploy/k8s/r6-stage/newhub-prometheus-rule.yaml` 是真源;
指标由 `/metrics` 暴露、Netdata go.d `prometheus` collector 主动抓
(**禁为换监控栈改业务代码**)。

## 排障

- **滚动更新卡住**:`kubectl describe pod -l app=lurus-newhub -n lurus-newhub | grep -A10 Events`
  —— 多半是 readiness 深检没过(先看 `/api/health` 哪一项红)。
- **部署后重启循环**:先分清是**进程坏**还是**依赖抖**;liveness 故意保持浅检就是为了后者不触发重启。
- **Session 在部署后失效**:确认 `SESSION_SECRET` 来自同一 Secret,且 Redis 可达。
- **副本数与预期不符**:先看 ArgoCD 是否 Synced —— 手工改过的副本数会被 selfHeal 拉回 git 值。
