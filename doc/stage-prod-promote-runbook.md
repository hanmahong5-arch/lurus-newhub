# Newhub STAGE→PROD promote & owner-gated steps

Status date: 2026-07-16. Written after the enterprise-uplift hardening batch
(security IDOR/auth fixes, boot-lock crash-loop fix, structural isolation
guards) landed on `main` and deployed to STAGE R6.

This runbook lists the procedures that move real state or touch near-production
infra — deliberately left for an operator with the credentials and the go/no-go
call. Nothing here has been executed against PROD (R1).

---

## 1. STAGE is current

- STAGE R6 (`ssh root@100.122.83.20`, ns `lurus-newhub`) runs `main@b6d96c91`
  (image digest `sha256:718f31f7…`).
- Verified: 3 pods Running, restart count 0 (was 15–16 crash-looping before the
  boot advisory-lock fix), `schema_migrations = 28`, `/api/health` healthy.
- `BILLING_UNIFIED_ENABLED=false` (advisory/legacy billing), `OIDC_ENABLED=false`,
  `MEILISEARCH_ENABLED=false`.

## 2. Roll STAGE forward to a newer main

1. Merge PRs to `main`; the `Publish Docker image (main branch)` workflow builds
   `ghcr.io/hanmahong5-arch/lurus-newhub:main`.
2. On the R6 node resolve the immutable digest (also warms the node):
   `crictl pull ghcr.io/hanmahong5-arch/lurus-newhub:main && crictl inspecti … | grep repoDigests`
3. Update `deploy/k8s/r6-stage/deployment.yaml` `image:` to the new digest, commit.
4. `kubectl diff -f deploy/k8s/r6-stage/deployment.yaml` — confirm the diff is
   image-only (watch for out-of-band env drift, e.g. `ALLOWED_ORIGINS`).
5. `kubectl apply -f -` (pipe the manifest over SSH), then
   `kubectl rollout status deployment/lurus-newhub -n lurus-newhub`.
6. Post-checks: pods Running with restart 0, `schema_migrations` count matches
   the migration file count, `/api/health` = healthy.

Do NOT `kubectl apply -k deploy/k8s/r6-stage/` — that would apply
`secret-template.yaml`'s placeholders over the real secret. Apply
`deployment.yaml` alone.

## 3. STAGE→PROD (R1) promote — NOT a DNS flip

R1's shadow identity/newhub stack was retired 2026-06-23; promotion is a fresh,
deliberate deploy, not a cutover. Preconditions before any R1 action:

- Owner go/no-go (R1 carries live commercial traffic).
- A PROD Postgres with the `newhub` database (tables in the `public` schema — the
  `lurus_api` schema named in older docs does not exist) and its own secret
  (`SESSION_SECRET`, `SQL_DSN`, `IDENTITY_*`, `LURUS_WHITELABEL_MASTER_SECRET`).
- Migrations run once against PROD PG (boot runner does this under the advisory
  lock; the lock wait + DDL are now statement_timeout-exempt, so a multi-replica
  first boot no longer crash-loops).
- PROD manifest pinned to the same digest verified on STAGE.
- Rollback: keep the prior digest; `kubectl rollout undo` reverts pods, but a
  migration that already ran is forward-only — take a PG backup first.

## 4. Arm SEAM S1 (platform → newhub credit-pool fund)

The code landmine is fixed: platform now sends `X-API-Key` (not `Bearer`) and
gates the module on `NEWHUB_API_KEY` (lurus-platform PR #49, merged). To arm the
chain on STAGE (moves real ledger state — do it watched):

1. Mint a newhub internal key with scopes `balance:write` (fund) and
   `user:delete` (PIPL erase). It is stored SHA-256-hashed
   (`repo.CreateInternalApiKey`); keep the plaintext `lurus_ik_…` for step 3.
2. Redeploy platform-core on R6 to the image carrying PR #49 (near-prod
   platform-core also fronts `identity.lurus.cn` — coordinate).
3. Set on platform-core: `NEWHUB_BASE_URL=http://lurus-newhub.lurus-newhub.svc:8850`
   and secret `NEWHUB_API_KEY` = the minted key.
4. E2E drill: trigger a plan-change/topup on platform → confirm the fund POST to
   `/internal/v1/provisioning/tenants/:slug/credit-pool/fund` returns 200 and the
   newhub credit pool balance increases; `credit_pool_fund_events` dedups on
   `event_id`.

Follow-up: a third platform module (card-minter) has the same Bearer→X-API-Key
exposure (flagged in PR #49); give it the same fix before it goes live.

## 5. Enable OIDC SSO (user-facing login)

STAGE has no OIDC client credentials (`OIDC_CLIENT_ID/ISSUER/SECRET` absent;
`OIDC_ENABLED=false`). The newhub↔platform *internal* integration
(`IDENTITY_SERVICE_INTERNAL_KEY`) is separate and already works. To enable SSO:

1. Register a newhub OIDC client on the IdP (`identity.lurus.cn`) — IdP-admin
   gated. Capture client id/secret and the issuer URL.
2. Add `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` to `lurus-newhub-secrets`; set
   `OIDC_ISSUER` and `OIDC_ENABLED=true` in the deployment env.
   `ALLOWED_ORIGINS` already includes `https://identity.lurus.cn`.
3. Validate the auth-code callback end to end before relying on it.

Meilisearch (log full-text search) is optional: needs an in-cluster Meili plus
`MEILISEARCH_ENABLED=true` + host/key. Not a delivery blocker.

## 6. Platform DR-backup hardening (owner)

lurus-platform PRs #42 (deploy-ready pg-backup CronJob) and #43 (restore-drill
`--db` + hostPath DR tooling) are open but ~122 commits behind `master` — a
sizeable rebase on near-prod backup infra. Apply path: rebase each onto current
`master`, re-run the DR e2e, apply on R6 (writes only under `/data`), GRANT the
backup role on all DBs, then run a real restore drill. Owner-coordinated.

## 7. Upstream sync (owner)

Fork `origin/main` is 337 commits ahead of `LurusTech/lurus-hub` (`upstream`) and
`upstream` is behind by 0 — a clean fast-forward. Pushing to the shared,
owner-controlled upstream is a high-blast-radius publish:
`git push upstream origin/main:main`. Owner decision.
