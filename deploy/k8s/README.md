# Kubernetes 部署（lurus-newhub）

本目录只有两个活体：

| 路径 | 作用 |
|------|------|
| `r6-stage/` | **唯一的 newhub manifest**（ns `lurus-newhub`,`test-newhub.lurus.cn`) |
| `argocd/` | ArgoCD Application 定义,`path: deploy/k8s/r6-stage` |

> 2026-08-24 删除了同级的 `deployment.yaml` / `service.yaml` / `ingress.yaml` /
> `hpa.yaml` / `pdb.yaml` / `servicemonitor.yaml` / `secrets.yaml` /
> `meilisearch.yaml` / `kustomization.yaml`。它们描述的是 **2026-04-23 退役的
> `lurus-api`**（ns `lurus-system`、镜像名 `lurus-api`、secret `lurus-api-secrets`),
> 集群里已无任何对应对象,而本文件旧版还在教人 `kubectl apply -k deploy/k8s/` ——
> 那会把退役镜像部署进一个正跑着 redis/memorus 的命名空间,并注入一个**不存在的
> 出站代理地址**。需要历史内容从 git 历史取。

## 部署流程（不要手工 apply）

```
merge 到 main
  → docker-image-main.yml 构建 :main 镜像
  → bump_r6_manifest job 自动改写 r6-stage/deployment.yaml 的 `# pin:` 与 `image:`
    （[skip ci] commit)
  → ArgoCD Application（automated + selfHeal,prune off)自动收敛
```

**禁止** `kubectl set image` / `kubectl rollout restart` / `kubectl apply -k`——
selfHeal 会把手工改动回滚,只会制造"改了没生效"的假象。
应急路径与回滚见 `../../doc/runbook/staging-deploy.md`;**回滚 = revert 那次 pin commit**。

ArgoCD 不可用时的后备：`SKIP_SECRETS=1 bash scripts/deploy-stage.sh`。

## Secret

`lurus-newhub-secrets` **不在 git 里**。`r6-stage/secret-template.yaml` 只是 schema 文档,
且**故意不是** kustomize resource——它曾经是,导致每次 apply 都把真 Secret 覆盖成
`<set-via-kubectl>` 字面量（2026-08-15 移除)。创建/轮换的一行命令写在该文件头部,
取值见仓库根 `重要信息.md`。

一个例外：`OIDC_CLIENT_ID` **不要手填**。它由 platform 的 app registry
（`2l-svc-platform/config/apps.yaml` 的 `newhub` 条目)在 IdP 注册后回写进这个 Secret,
5 分钟一轮的 reconciler 负责收敛。手填的值会被覆盖。

## 出站网络

relay 到上游供应商**直连 :443,没有也不要加集群级 `HTTP_PROXY`**。
理由、实测结论、以及被墙供应商的正确做法（per-channel proxy + ns `lurus-system` 内的
egress Service)全部写在 `r6-stage/netpol-nats-egress.yaml` 的头注释里,加代理前先读。

## 验证

```bash
# R6：Tailscale 主路径,备用 ssh -p 12222 root@43.226.45.87
ssh root@100.122.83.20 "kubectl get pods -n lurus-newhub"
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deploy/lurus-newhub --tail=100"

# 健康：database / redis / billing / schema_migrations 四检
curl -s https://test-newhub.lurus.cn/api/health

# migration 漂移信号
curl -s https://test-newhub.lurus.cn/metrics | grep schema_migrations
```

## 相关文档

- 部署 runbook：`../../doc/runbook/staging-deploy.md`
- 部署落点 ADR：`../../doc/decisions/2026-08-23-deploy-canonical-r6-stage.md`
- 数据库运维：`../../doc/runbook/database.md`
- 凭证：仓库根 `重要信息.md`（禁提交)
