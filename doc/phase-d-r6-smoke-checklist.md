# Phase D R6 STAGE Smoke Checklist (2026-05-19)

> Switch Phase D push 后的 R6 STAGE 部署 + e2e smoke。Point-in-time snapshot; pod-name/commit-lag details distilled out — reusable activation + smoke commands kept.
>
> **As-of correction 2026-09-20:** the deploy namespace is **`lurus-newhub`**.
> A 2026-06-13 note here said it had moved to `lurus-staging` because the PG
> `pg-access-control` netpol supposedly only whitelisted that namespace; that
> netpol no longer exists (`kubectl get netpol -n database` is empty, verified
> 2026-08-23) and that namespace never materialised on R6 at all, so every
> command printed here under it could only ever fail. The `-n` flags below now
> read `lurus-newhub`. The isolated UAT instance added 2026-08-30 is
> a different namespace again, `lurus-newhub-uat` (NodePort 30851). Deployment
> name (`lurus-newhub`), image, secret (`lurus-newhub-secrets`), and the R6
> Tailscale host `100.122.83.20` are unchanged (verified). For current deploys
> prefer `scripts/deploy-stage.sh` (see `doc/runbook/staging-deploy.md`).

## R6 facts

| 项 | 值 |
|---|---|
| Hub 部署 | k3s, namespace `lurus-newhub`, deployment `lurus-newhub` |
| Image | `ghcr.io/hanmahong5-arch/lurus-newhub:main`(浮动 tag, `imagePullPolicy: Always`) |
| K8s service | `lurus-newhub` NodePort 8850→30850 |
| 容器 port | 3000 (`/api/status` health) |
| Nginx site | `test-newhub.lurus.cn` → `127.0.0.1:30850` (443/80) |
| Hub URL | `https://test-newhub.lurus.cn` |
| set env (已) | `SESSION_SECRET`, `SQL_DSN`, `IDENTITY_SERVICE_INTERNAL_KEY`, `IDENTITY_SESSION_SECRET`, `IDENTITY_GRPC_ADDR`, `NATS_URL`, `ALLOWED_ORIGINS=https://test-newhub.lurus.cn` |
| **`LURUS_WHITELABEL_MASTER_SECRET`** | **UNSET** (deployment env + secret `lurus-newhub-secrets` 均无) — **阻塞 hmac-key endpoint** |

**已知 CI 阻塞** (在 `3d03eeeb`): `Go CI` fail = workflow checkout 缺兄弟 repo `shared/lurus-proto-go`(`Repository path '...shared/lurus-proto-go' is not under '...lurus-newhub'`);`Build and Push Docker Image` fail = Dockerfile 引用不存在的 `/zita-sdk-go`。**但** `Publish Docker image (main branch)` workflow **success**(不含缺失的 SDK 依赖,产物镜像可用)。

## 部署到 R6

### Step A — 加 `LURUS_WHITELABEL_MASTER_SECRET`

```bash
SECRET_VAL="$(openssl rand -hex 32)"
# 写入 secret(不自动 rollout):
ssh root@100.122.83.20 "kubectl patch secret -n lurus-newhub lurus-newhub-secrets --type=json -p='[{\"op\":\"add\",\"path\":\"/data/LURUS_WHITELABEL_MASTER_SECRET\",\"value\":\"'$(echo -n "$SECRET_VAL" | base64 -w0)'\"}]'"
# 挂到 deployment env(触发 rollout):
ssh root@100.122.83.20 "kubectl patch deployment -n lurus-newhub lurus-newhub --type=json -p='[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/env/-\",\"value\":{\"name\":\"LURUS_WHITELABEL_MASTER_SECRET\",\"valueFrom\":{\"secretKeyRef\":{\"name\":\"lurus-newhub-secrets\",\"key\":\"LURUS_WHITELABEL_MASTER_SECRET\"}}}}]'"
```

> 三铁律提醒: `kubectl patch` 仅临时验证;长期方案 = 找到 newhub GitOps manifest 源 repo(当前无 ArgoCD app for newhub),改 git 后 `kubectl apply -f`。

### Step B-E — rollout + 验证

```bash
ssh root@100.122.83.20 "kubectl rollout restart deployment/lurus-newhub -n lurus-newhub"
ssh root@100.122.83.20 "kubectl rollout status deployment/lurus-newhub -n lurus-newhub --timeout=120s"
# 验证新 pod 起新 image(Started > image push 时间, Image ID 变化):
ssh root@100.122.83.20 "kubectl describe pod -n lurus-newhub \$(kubectl get pods -n lurus-newhub -l app=lurus-newhub -o name | tail -1 | cut -d/ -f2) | grep -E '(Image ID:|Started:)'"
# startup log(期望 'listening on :3000' 无 panic):
ssh root@100.122.83.20 "kubectl logs -n lurus-newhub deployment/lurus-newhub --tail=80"
# GHCR :main 当前 digest:
gh api /users/hanmahong5-arch/packages/container/lurus-newhub/versions --jq '.[0:3]'
```

## E2E smoke (本地 Git Bash 直连域名)

```bash
HUB_URL="https://test-newhub.lurus.cn"

# 1. 健康检查 + endpoint 存活
curl -sfk "$HUB_URL/api/status"                          # JSON, 含 version/start_time
curl -sfk "$HUB_URL/api/v2/switch/tools/versions"        # 列 claude/codex/gemini 等工具版本

# 2. Redeem(匿名,用 Reseller 控制台新生成的真实激活码)
TEST_CODE="<新激活码>"
curl -sk -X POST "$HUB_URL/api/v2/switch/redeem" -H "Content-Type: application/json" \
  -d "{\"code\":\"$TEST_CODE\",\"fingerprint\":\"smoke-$(date +%s)\",\"app_version\":\"smoke\"}" | jq
# 成功: {"success":true,"data":{"user_token":"sk-...","user_id":...,"quota":...,"tenant_slug":"..."}}
# 已用: {"success":false,"message":"该兑换码已使用"}  · 404: router 没注册(镜像旧, 回 Step B)

# 3. Heartbeat(用上一步 user_token + tenant_slug)
USER_TOKEN="<user_token>"; TENANT_SLUG="<tenant_slug>"
curl -sk -X POST "$HUB_URL/api/v2/$TENANT_SLUG/user/heartbeat" -H "Authorization: $USER_TOKEN" \
  -H "Content-Type: application/json" -d "{\"fingerprint\":\"smoke-$(date +%s)\",\"app_version\":\"smoke\"}" | jq
# single-tenant fallback: POST "$HUB_URL/api/v2/switch/heartbeat" (同 header/body)
# 期望: {"success":true,"data":{"status":"active","quota":...}}

# 4. HMAC key(admin auth)
ADMIN_TOKEN="<Hub admin session token>"; TENANT_SLUG="<经销商 slug>"
curl -sfk "$HUB_URL/api/v2/admin/whitelabel/hmac-key?tenant_slug=$TENANT_SLUG" -H "Authorization: Bearer $ADMIN_TOKEN" | jq
# 期望: {"success":true,"data":{"hmac_key":"<64 hex>"}}  · 500: SECRET 没设(回 Step A) · 401: token 错 · 404: router 没注册
```

## Switch wails dev EndUser smoke

```bash
cd /c/Users/Anita/Desktop/lurus/2c-gui-switch && wails dev
```
GUI: AppModeSelect → **EndUser** → 填 Hub URL `https://test-newhub.lurus.cn` → ActivationPage 输入激活码 → Activate → 验证 toast + 跳 EndUserMainPage(显 quota/expires)→ ~60s 看 stdout `heartbeat status=active` → 关闭重启验证 token 持久化(不需重新激活)。

## 失败排查

| 症状 | 可能原因 |
|---|---|
| Redeem 404 | router 没注册 / pod 旧镜像 → `kubectl rollout restart` |
| Redeem 500 connection refused | nginx 通但 pod startup probe 没过 → 看 `kubectl logs` panic |
| Heartbeat 401 | user_token 错 / token 被禁 / 漏 `Bearer ` 前缀(rawToken 模式不需要) |
| HMAC key 500 | `LURUS_WHITELABEL_MASTER_SECRET` 没设(回 Step A) |
| HMAC key 401 | admin token 不对 |
| Switch UI 白屏 | bun build 没跑;`wails build` 或 `cd frontend && bun install && bun run build` |

## 已知限制

- CI 失败遗留: Go CI / Docker Build workflow fail(checkout 缺 `shared/lurus-proto-go` + Dockerfile 引用不存在的 `/zita-sdk-go`)。`Publish Docker image (main branch)` 不受影响,镜像可用。下个 Sprint 修。
- `*claw`(picoclaw/nullclaw/openclaw/zeroclaw)`buildLaunchArgs` 仅生成 nil args, TODO 待验证。claude/codex/gemini `--model` flag 未实跑 `--help` 验证。
- R6 `hub.lurus.cn` nginx site **未配**;CLAUDE.md 里 Personal 模式默认指 `hub.lurus.cn`,实测要用 `test-newhub.lurus.cn`(或 R6 加 nginx site)。
- 无 ArgoCD app for newhub(`kubectl get applications -A` 只有 lucrum-web);GitOps 源 manifest 位置未确认 → 本 checklist 用 `kubectl patch` 临时改;长期 = 找 manifest 源 repo, 把 secret 改进 git 后 `kubectl apply -f`。
