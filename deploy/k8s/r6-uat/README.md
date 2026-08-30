# r6-uat — isolated UAT instance

**Why this exists**: until 2026-08-30, `test-newhub.lurus.cn` and
`hub.lurus.cn` proxied to the SAME NodePort 30850 — same process, same DB.
There was no environment where UAT/e2e could run without touching production
(UAT 2026-08-26 structural finding). This overlay is that environment.

| | prod (`r6-stage/`) | uat (this overlay) |
|---|---|---|
| Namespace | `lurus-newhub` | `lurus-newhub-uat` |
| Domain | `hub.lurus.cn` | `test-newhub.lurus.cn` (since 2026-08-30 cutover) |
| NodePort | 30850 | **30851** (also reachable host-local/tunnel) |
| PostgreSQL | db `newhub` | db `newhub_uat` (role `newhub_uat`) |
| Redis | DB 2 | DB 3 |
| OIDC | on | **off** (no IdP client for UAT; bridge login instead) |
| Billing unified | on | **off** (never debit the real platform wallet) |
| NATS quota events | on | **off** (never pollute LLM_EVENTS) |
| E2E_BRIDGE_TOKEN | absent (route not registered) | set → `/api/v2/bridge/exchange` live |
| Replicas | 3 | 1 |
| Image digest | auto-pinned | **same digest, same auto-pin job** |

- Secret `lurus-newhub-uat-secrets` (SESSION_SECRET / SQL_DSN /
  LURUS_WHITELABEL_MASTER_SECRET / E2E_BRIDGE_TOKEN): created 2026-08-30 with
  values generated ON the R6 host (`openssl rand`), never stored off-host.
  Rotate the same way.
- Convergence: ArgoCD Application `lurus-newhub-uat`
  (`deploy/k8s/argocd/application-uat.yaml`), automated + selfHeal, prune off.
- Reaching it: `https://test-newhub.lurus.cn` (host-nginx vhost →
  NodePort 30851, source `deploy/r6-host-nginx/test-newhub.conf`). The tunnel
  path `ssh -p 12222 -L 30851:localhost:30851 root@43.226.45.87` (or Tailscale
  `100.122.83.20`) still works for API-level access, but since `SESSION_SECURE`
  went `true` (2026-08-30) browsers DROP the session cookie over plain-http
  localhost — use the domain for browser/e2e flows.
- Fresh-DB bootstrapping is the product's own: GORM auto-migrate + embedded
  migration runner; migration 021 self-seeds the default tenant. First boot
  seeds the root user like any fresh install. Public-exposure hardening
  (2026-08-30): `RegisterEnabled=false` seeded in the `options` table (code
  default is open registration), bridge rejects bad tokens (403), and the
  `users` table carries no password column — password login is structurally
  absent, bridge is the only local login.
- ~~Owner-gated follow-up: pointing `test-newhub.lurus.cn` here~~ **DONE
  2026-08-30**: prod SSO moved to `hub.lurus.cn` (r6-stage deployment env +
  platform `config/apps.yaml` domain PATCH, client_id unchanged), then the
  vhost flipped 30850 → 30851. Nightly e2e runs against this instance via
  `.github/workflows/web-ci.yml` (schedule 19:00 UTC).
