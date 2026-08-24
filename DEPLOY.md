# 部署 — 见 `doc/runbook/staging-deploy.md`

**真源：[`doc/runbook/staging-deploy.md`](./doc/runbook/staging-deploy.md)**。

本文件 2026-08-24 清空。原内容是 **2026-04-23 退役的 `lurus-api`** 的手工部署指南
（ns `lurus-system`、host `api.lurus.cn`、DB `lurusapi`、PG `100.94.177.10:30543`、
`secrets.prod.yaml`、写死的 `OIDC_CLIENT_ID`），对 `2b-svc-newhub` 逐项皆错。

三点现在必须反着做：

1. **不要手工 apply secret 再 `kubectl rollout restart`**——`lurus-newhub` 归 ArgoCD
   （automated + selfHeal）管，手工滚动会被回滚。部署 = merge main，CI 自动 auto-pin
   `deploy/k8s/r6-stage`，ArgoCD 收敛。
2. **不要手填 `OIDC_CLIENT_ID`**——由 platform app registry 在 IdP 注册后回写进
   `lurus-newhub-secrets`，手填会被 reconciler 覆盖。
3. **回滚 = revert auto-pin commit**，不是 `kubectl rollout undo`。

Secret 的字段清单与创建命令见 `deploy/k8s/r6-stage/secret-template.yaml` 头部，
取值见仓库根 `重要信息.md`（禁提交）。

支持：代码 `doc/code-review/README.md` · 运维 `doc/runbook/incident-response.md`。
