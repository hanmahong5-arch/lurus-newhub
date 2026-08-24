# Deployment Runbook — 已移至 `staging-deploy.md`

**真源：[`doc/runbook/staging-deploy.md`](./staging-deploy.md)**（构建/部署/验证/回滚/应急路径全在那里）。

本文件 2026-08-24 清空。原内容描述的是 **2026-04-23 退役的 `lurus-api`**：
service `lurus-api` · ns `lurus-system` · host `api.lurus.cn` · ssh `100.98.57.55` ·
镜像 `ghcr.io/LurusTech/lurus-api` · secret `lurus-api-secrets` —— **每一项对
`2b-svc-newhub` 都是错的**，且它主推 `kubectl set image` / `kubectl rollout restart` /
`kubectl apply -k deploy/k8s/` 三条现在会被 ArgoCD selfHeal 直接回滚的操作
（`deploy/k8s/` 下那套 base manifest 已于同日删除）。需要历史内容从 git 历史取。

现状速查：

| 项 | 值 |
|----|-----|
| Service / ns | `lurus-newhub` / `lurus-newhub` |
| Host | `test-newhub.lurus.cn`（STAGE=R6) |
| SSH | `root@100.122.83.20`（Tailscale;备用 `ssh -p 12222 root@43.226.45.87`) |
| 镜像 | `ghcr.io/hanmahong5-arch/lurus-newhub`（digest 钉版) |
| Secret | `lurus-newhub-secrets` |
| 部署 | merge main → CI 出 `:main` 并 auto-pin `deploy/k8s/r6-stage` → ArgoCD 收敛 |
| 回滚 | **revert 那次 auto-pin commit**,不是 `rollout undo` |

其余 runbook：`database.md`(备份/恢复/迁移)、`tenant-onboarding.md`(新租户,
**必须建 credit pool 行**,否则 `CREDIT_POOL_REQUIRED=enforce` 会 402)、
`incident-response.md`。
