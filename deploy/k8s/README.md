# Kubernetes Deployment

## 快速部署

### 1. 准备生产配置

```bash
cd deploy/k8s

# 复制 secrets 模板
cp secrets.yaml secrets.prod.yaml

# 编辑 secrets.prod.yaml
# 使用 ../../重要信息.md 中的凭证信息
vi secrets.prod.yaml
```

### 2. 填写 secrets.prod.yaml

**根据 `../../重要信息.md` 填写以下值**:

```yaml
stringData:
  # 数据库连接（参考：重要信息.md → 数据库 PostgreSQL）
  SQL_DSN: "postgres://lurus:<PG_PASSWORD,见 重要信息.md>@100.94.177.10:30543/lurusapi?sslmode=disable"

  # 生成新的 Session Secret（⚠️ 不要使用泄露的旧值）
  SESSION_SECRET: "$(openssl rand -base64 32)"

  # Alipay 密钥（联系支付团队获取）
  ALIPAY_PRIVATE_KEY: |
    <粘贴完整 PEM 私钥内容,从 Alipay 开发者控制台获取>

  # OIDC Client ID（参考：重要信息.md → 身份提供方）
  OIDC_CLIENT_ID: "<deploy-time-client-id>"
```

### 3. 生成新的 Session Secret

```bash
# 生成 32 字节的随机字符串
openssl rand -base64 32

# 或使用 Python
python3 -c "import secrets; print(secrets.token_urlsafe(32))"

# 将生成的值填入 secrets.prod.yaml 的 SESSION_SECRET
```

### 4. 部署到集群

```bash
# 应用 secrets
kubectl apply -f secrets.prod.yaml

# 应用 deployment
kubectl apply -f deployment.yaml

# 应用 service
kubectl apply -f service.yaml

# 验证部署
kubectl get pods -n lurus-system
kubectl logs -f deployment/lurus-api -n lurus-system
```

---

## 重要说明

### ⚠️ 安全要求

1. **禁止提交 secrets.prod.yaml 到 Git**
   - 已添加到 `.gitignore`
   - 仅保留 `secrets.yaml` 模板

2. **Session Secret 必须更换**
   - 旧的 secret 已在 Git 历史中泄露
   - 生成新的随机值（至少 32 字节）

3. **数据库密码保持公司标准**
   - 使用 `重要信息.md` 中的统一密码
   - 密码: `<PG_PASSWORD,见 重要信息.md>`（PostgreSQL lurus 用户）

### 📖 凭证参考

所有生产凭证请参考根目录的 `重要信息.md`:

```
lurus/
├── 重要信息.md          ← 🔐 包含所有凭证（DO NOT COMMIT）
├── lurus-api/
│   └── deploy/k8s/
│       ├── secrets.yaml      ← 模板（可提交）
│       └── secrets.prod.yaml ← 生产配置（禁止提交）
```

---

## 数据库连接

### 选项 1: 通过 NodePort（推荐，外部访问）

```yaml
SQL_DSN: "postgres://lurus:<PG_PASSWORD,见 重要信息.md>@100.94.177.10:30543/lurusapi?sslmode=disable"
```

- **Host**: `100.94.177.10` (database 服务器 Tailscale IP)
- **Port**: `30543` (NodePort)
- **优点**: 可从集群外访问（开发、调试）

### 选项 2: 通过 ClusterIP（生产推荐）

```yaml
SQL_DSN: "postgres://lurus:<PG_PASSWORD,见 重要信息.md>@lurus-pg-rw.database.svc:5432/lurusapi?sslmode=disable"
```

- **Host**: `lurus-pg-rw.database.svc` (K8s Service DNS)
- **Port**: `5432` (默认 PostgreSQL 端口)
- **优点**: 集群内部通信，更快、更安全

---

## 环境变量

所有配置通过 Secret 和 ConfigMap 注入，无需修改代码。

### 从 Secret 注入（敏感信息）

- `SQL_DSN` - 数据库连接字符串
- `SESSION_SECRET` - Session 加密密钥
- `ALIPAY_PRIVATE_KEY` - 支付宝私钥
- `ALIPAY_PUBLIC_KEY` - 支付宝公钥
- `OIDC_CLIENT_ID` - OIDC OAuth 客户端 ID（部署期注入）

### 从 ConfigMap/Environment 注入（公开配置）

- `REDIS_ADDR` - Redis 地址
- `MEILISEARCH_URL` - Meilisearch 地址
- `LOG_LEVEL` - 日志级别
- `GIN_MODE` - Gin 运行模式

---

## 故障排查

### Pod 无法启动

```bash
# 查看 Pod 状态
kubectl get pods -n lurus-system

# 查看详细事件
kubectl describe pod <pod-name> -n lurus-system

# 查看日志
kubectl logs <pod-name> -n lurus-system

# 常见问题：
# 1. Secret 未创建 → kubectl apply -f secrets.prod.yaml
# 2. 镜像拉取失败 → 检查 GHCR 认证
# 3. 数据库连接失败 → 检查 SQL_DSN 是否正确
```

### 数据库连接失败

```bash
# 从 Pod 内测试数据库连接
kubectl exec -it <pod-name> -n lurus-system -- /bin/sh
nc -zv 100.94.177.10 30543

# 或使用 psql（如果镜像包含）
psql "postgres://lurus:<PG_PASSWORD,见 重要信息.md>@100.94.177.10:30543/lurusapi?sslmode=disable" -c "SELECT 1"
```

### Secret 更新不生效

```bash
# 更新 Secret
kubectl apply -f secrets.prod.yaml

# ⚠️ 必须重启 Pod 才能加载新 Secret
kubectl rollout restart deployment/lurus-api -n lurus-system

# 验证新 Pod 已启动
kubectl get pods -n lurus-system -w
```

---

## 回滚

```bash
# 查看部署历史
kubectl rollout history deployment/lurus-api -n lurus-system

# 回滚到上一个版本
kubectl rollout undo deployment/lurus-api -n lurus-system

# 回滚到指定版本
kubectl rollout undo deployment/lurus-api -n lurus-system --to-revision=2
```

---

## ArgoCD GitOps 部署

如果使用 ArgoCD，更新流程：

```bash
# 1. 手动应用 secrets（不通过 Git）
kubectl apply -f secrets.prod.yaml

# 2. 提交代码到 Git
git add deployment.yaml service.yaml
git commit -m "feat: update deployment configuration"
git push origin main

# 3. ArgoCD 自动同步
# 访问: https://argocd.lurus.cn
# 或手动触发: argocd app sync lurus-api
```

---

## 相关文档

- **主文档**: `../../README.md`
- **凭证信息**: `../../重要信息.md` (DO NOT COMMIT)
- **数据库运维**: `../../doc/runbook/database.md`
- **部署流程**: `../../doc/runbook/deployment.md`
- **代码审查**: `../../doc/code-review/2026-02-11-code-review.md`
