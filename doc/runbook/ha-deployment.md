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
| replicas | **3**(leader election 演练:杀掉 leader,看备用在 lease TTL 内接管——用 `lurus_gateway_leader` gauge/`checks.leader`,配 `/metrics` 的 `lurus_gateway_instance_info` 的 `pod` label 区分哪个副本;`/api/health` 是公网端点,不带 pod 身份,见下方演练命令) |
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

## leader election 演练

通过 `/metrics` 鉴权闸门的抓取会带 `X-Lurus-Instance` 头(值 = pod 名,downward API
`POD_NAME`;被闸门拒掉的抓取不带),同一个 pod 身份也作为 `lurus_gateway_instance_info`
的 `pod` label 出现在响应体里,据此把 `lurus_gateway_leader` gauge 归到具体副本;
`/api/health` 只暴露粗粒度的
`checks.leader`(`held`|`standby`,不带 pod 身份——该端点公网可达,无鉴权网关)——
`standby` 是跟随者的正常态,**不会**被判 degraded/unhealthy。

🔴 一个被降级的副本会**永远**保留它最后一次成功时打的
`lurus_gateway_leader_task_last_success_timestamp_seconds{task}` 时间戳(该 series 不会因
降级而清零或消失),所以任何基于它的告警(`time() - last_success > X`)必须同时限定
`lurus_gateway_leader == 1`,否则一个早已下台的副本的陈旧时间戳会一直压着告警不触发。

```bash
# 逐 pod 直连 NodePort(host nginx 只转发一个 Service,单次 curl 落在哪个副本不确定)
for p in $(kubectl get pods -n lurus-newhub -l app=lurus-newhub -o jsonpath='{.items[*].metadata.name}'); do
  kubectl exec -n lurus-newhub "$p" -- wget -qO- http://localhost:3000/api/health | grep -o '"leader":"[^"]*"'
done
# 或直接看 pod 自己的 /metrics。instance_info 的 pod label 在响应体里,不依赖看响应头,
# 所以这一条同时给出"是哪个副本"和"它是不是 leader"
kubectl exec -n lurus-newhub <leader-pod> -- wget -qO- http://localhost:3000/metrics \
  | grep -E '^lurus_gateway_(leader |instance_info)'

# 杀掉持锁的那个副本,在 lease TTL 内应看到另一个副本的 lurus_gateway_leader 从 0 变 1
kubectl delete pod -n lurus-newhub <leader-pod>
```

## 告警阈值

监控栈已切 Netdata 自托管:指标由 `/metrics` 暴露、Netdata go.d `prometheus`
collector 主动抓(**禁为换监控栈改业务代码**)。告警阈值若存在,只会在 R6 主机侧的
netdata 配置里——本 repo 不跟踪它,也不能证明任何 newhub 阈值已经配好。`deploy/grafana/newhub-alerts.yaml`(连同其余
`deploy/grafana/*`)已删除,不再是真源。`deploy/k8s/r6-stage/newhub-prometheus-rule.yaml`
仍保留在 repo 里,但文件头已标注 **NOT DEPLOYED**——它不在
`deploy/k8s/r6-stage/kustomization.yaml` 的 `resources:` 列表里,且 R6 未跑
Prometheus Operator,所以没有任何东西在求值这些规则;只作为「曾经决定值得告警」的记录留存。

## 排障

- **滚动更新卡住**:`kubectl describe pod -l app=lurus-newhub -n lurus-newhub | grep -A10 Events`
  —— 多半是 readiness 深检没过(先看 `/api/health` 哪一项红)。
- **部署后重启循环**:先分清是**进程坏**还是**依赖抖**;liveness 故意保持浅检就是为了后者不触发重启。
- **Session 在部署后失效**:确认 `SESSION_SECRET` 来自同一 Secret,且 Redis 可达。
- **副本数与预期不符**:先看 ArgoCD 是否 Synced —— 手工改过的副本数会被 selfHeal 拉回 git 值。
