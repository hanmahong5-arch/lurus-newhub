# 快速部署指南

> 5 分钟生产部署。完整运维(CI/CD、回滚、resource、ArgoCD)见 `doc/runbook/deployment.md`。

## 部署前检查

- [x] 代码已编译 + 测试通过
- [ ] 生成新 Session secret(旧值已泄露,必须换)
- [ ] 填写 `secrets.prod.yaml`(参考 `../../重要信息.md`)

## 快速部署

```bash
# 1. 生成 Session Secret
openssl rand -base64 32

# 2. 创建生产配置
cd deploy/k8s && cp secrets.yaml secrets.prod.yaml && vi secrets.prod.yaml
```

`secrets.prod.yaml` stringData 关键字段:

```yaml
SQL_DSN: "postgres://lurus:LurusOps2026@100.94.177.10:30543/lurusapi?sslmode=disable"
SESSION_SECRET: "<openssl rand -base64 32 输出>"
ALIPAY_PRIVATE_KEY: |    # 从 Alipay 开发者控制台获取(如需支付)
  -----BEGIN RSA PRIVATE KEY-----
ALIPAY_PUBLIC_KEY: |
  -----BEGIN PUBLIC KEY-----
OIDC_CLIENT_ID: "358371335178617311@lurus-api"
```

```bash
# 3. 部署
kubectl apply -f secrets.prod.yaml
kubectl rollout restart deployment/lurus-api -n lurus-system
kubectl get pods -n lurus-system -w

# 4. 验证
kubectl wait --for=condition=ready pod -l app=lurus-api -n lurus-system --timeout=120s
curl https://api.lurus.cn/api/status                                                    # {"status":"ok"}
curl "https://api.lurus.cn/api/v1/releases/latest/lurus-cli?current_version=1.9.0"      # has_update:true (semver fix)
curl https://api.lurus.cn/bind/alipay?code=test                                          # {"success":false,"message":"未登录或会话已过期"}
```

## 凭证说明

- **数据库密码** `LurusOps2026` — **无需更换**(内部统一标准,虽曾在 git 历史泄露但已清理,见 `doc/code-review/README.md` P0-1)。
- **Session Secret** — **必须更换**;旧值 `LurusApiSessionSecret2026Secure!` 已泄露。`openssl rand -base64 32` 生成。Secret 更新后**必须** `kubectl rollout restart` 才生效。
- **Alipay 密钥** — 联系支付团队获取 App ID + RSA2 私钥/公钥。

## 故障排查

```bash
kubectl describe pod <pod> -n lurus-system          # 常见:Secret YAML 缩进 / SQL_DSN 格式 / GHCR 认证
kubectl logs <pod> -n lurus-system
kubectl exec -it <pod> -n lurus-system -- nc -zv 100.94.177.10 30543   # DB 连通性
psql "postgres://lurus:LurusOps2026@100.94.177.10:30543/lurusapi?sslmode=disable" -c "SELECT version()"
```

## 回滚

```bash
kubectl rollout undo deployment/lurus-api -n lurus-system
kubectl rollout history deployment/lurus-api -n lurus-system
```

支持: 代码 `doc/code-review/README.md` · 运维 `doc/runbook/incident-response.md` · 凭证 `../../重要信息.md`。
