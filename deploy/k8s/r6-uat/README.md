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
- **Signing in from a browser**: `https://test-newhub.lurus.cn/bridge-login`.
  With single sign-on off, `/login` used to redirect into
  `/api/v2/auth/zita-login`, which answers 503 here — a dead end with no way
  back, so the only way in was pasting a fetch into the devtools console. That
  page now asks `/api/status` first and offers this route instead. Paste the
  token, or open `/bridge-login#t=<E2E_BRIDGE_TOKEN>` to sign in with one
  click (`&u=<user_id>` picks the user; it defaults to 1, the seeded root).
  A fragment never reaches the server or its logs, and the page strips it from
  the address bar once exchanged — it is still a credential in a link, so
  share it the way you would share the token itself. Read the token with:
  `ssh -p 12222 root@43.226.45.87 "kubectl get secret -n lurus-newhub-uat lurus-newhub-uat-secrets -o jsonpath='{.data.E2E_BRIDGE_TOKEN}' | base64 -d"`.
- ~~Owner-gated follow-up: pointing `test-newhub.lurus.cn` here~~ **DONE
  2026-08-30**: prod SSO moved to `hub.lurus.cn` (r6-stage deployment env +
  platform `config/apps.yaml` domain PATCH, client_id unchanged), then the
  vhost flipped 30850 → 30851. Nightly e2e runs against this instance via
  `.github/workflows/web-ci.yml` (schedule 19:00 UTC).

## Fault simulator (`FAULTSIM_TOKEN`) — task-vendor probe (cycle-8 L8)

`internal/adapter/handler/faultsim.go` registers `POST
/api/v2/faultsim/v1/chat/completions` (pre-existing) and, as of this lane,
`POST /api/v2/faultsim/suno/submit/:action` + `POST
/api/v2/faultsim/suno/fetch` — ONLY when the env var `FAULTSIM_TOKEN` is
non-empty (`handler.FaultSimEnabled()`; absent by default, including here —
grep-confirmed: `FAULTSIM_TOKEN` is not currently set in this overlay's
`deployment.yaml`, so none of the faultsim routes exist on live UAT today).
Enabling it is an **operator step** (generate the token value ON the R6
host, same convention as the other UAT secrets above; this is NOT wired up
by this lane, which only ships the route+handler code):

1. `ssh -p 12222 root@43.226.45.87` (or Tailscale `100.122.83.20`), generate
   a token with `openssl rand -hex 24`, and add it to
   `lurus-newhub-uat-secrets` as `FAULTSIM_TOKEN`.
2. Add `FAULTSIM_TOKEN` (from that secret key) to the deployment's env list
   in `deploy/k8s/r6-uat/deployment.yaml` and let ArgoCD converge (or patch
   live + commit the git source per the repo's K8s-manifest-is-truth rule).
3. Seed a UAT channel via the admin API (same recipe as the other UAT
   channels — admin API, not raw SQL):
   - `type`: `ChannelTypeSunoAPI` (36) — the vendor the simulator imitates.
   - `base_url`: `https://test-newhub.lurus.cn/api/v2/faultsim`. A loopback
     address does NOT work even though the pod could reach it: the channel
     validator refuses a private base URL outright (measured 2026-09-15 —
     "channel base_url rejected: private IP address not allowed: 127.0.0.1",
     the SSRF guard from cycle 6). The public host name goes out to the host
     nginx and comes back to the same pod's NodePort, which is allowed by
     `netpol-egress.yaml` (public 443) and lands in the same process because
     UAT runs one replica.
   - `key`: the `FAULTSIM_TOKEN` value from step 1 (the simulator accepts a
     bearer key the same way a real channel would send one).
   - `models`: any model name you'll pass as `"model"` in the submit body.
     Give it a price too (`POST /api/v2/<slug>/pricing` with an
     `If-Match-Pricing-Version` header): an unpriced model is rejected by the
     pre-consume price lookup before the relay reaches the simulator at all.
4. Round trip:
   - `POST https://test-newhub.lurus.cn/v1/tasks/suno` with
     `{"model":"<seeded model>","action":"MUSIC"}` and the caller's own
     bearer token (not FAULTSIM_TOKEN — that authenticates the
     simulator-as-upstream, not the relay caller) → `task_id`.
   - `GET https://test-newhub.lurus.cn/v1/tasks/suno/<task_id>` — SUCCESS
     within one poller tick (15s; `UpdateTaskBulkWithContext`), with two
     `data:` URL artefacts (text/plain and image) in the task's `data`
     field — no real Suno-compatible vendor key involved anywhere.
   - The same GET with a different user's token → 404 `Task not found`,
     byte-identical to a random `task_id`.
