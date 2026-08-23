# Wave-UAT Handbook — Lurus Newhub

**Scope**: Wave-UAT campaign (S0 + Sα + Sβ + Sδ + Sα2 + Sε). Generated 2026-05-26.

> **As-of update 2026-08-23 (ADR deploy-canonical-r6-stage):** the live
> namespace is **`lurus-newhub`** (the `lurus-staging` ns and its PG netpol
> never materialised on R6 and are retired). `deploy-staging.yml` and the
> `deploy/k8s/staging/` overlay are **deleted** — deploys converge via the
> ArgoCD Application in `deploy/k8s/argocd/` (merge → auto-pin → sync), with
> `SKIP_SECRETS=1 bash scripts/deploy-stage.sh` as the manual fallback (see
> `doc/runbook/staging-deploy.md`). §2 below is kept as the historical UAT
> record; do not execute its deploy steps verbatim.

---

## 1. Test Matrix

DoD items enumerated from the Wave-UAT plan. Status as of Sε completion.

| # | DoD Criterion | Proof Artifact | Status |
|---|---------------|---------------|--------|
| α1 | Handler smoke tests green | `go test ./internal/adapter/handler/ -run TestGetAuditEvents\|TestGetGovernance\|TestListTokensV2_Pagination` | PASS |
| α2 | Repo layer ≥60% coverage | `go test -coverprofile=cov.out ./internal/adapter/repo/...` → 60.1% | PASS |
| α3 | Cross-tenant isolation tests | `internal/adapter/repo/tenant_isolation_test.go` (10 tests) | PASS |
| α4 | 5 security projection structs (view-only on list/get endpoints) | `internal/adapter/handler/{token,channel,log,billing,billing_invoice}_view.go` | PASS |
| α5 | app layer ≥80% coverage | CI coverage-gate job measured `internal/app` **88.4%** hermetically on 2026-08-09 (run 31304357298); the "19.3% — not reachable hermetically" verdict below was superseded by the 2026-07-25 coverage corpus (PR #72). Gate raised 25 → 84. | PASS |
| α6 | Race detector: `go test -race ./...` passes | Audit doc: `_bmad-output/planning-artifacts/test-debt-findings.md` | PASS (Linux CI only) |
| α7 | internal/app coverage baseline tracked | Baseline captured and now gated at 84 (actual 88.4%) — no longer deferred | PASS |
| α8 | Race audit doc committed | `_bmad-output/planning-artifacts/test-debt-findings.md` | PASS |
| α9 | Coverage gate threshold calibrated | `go-ci.yml` coverage-gate job (18/58/19 at α9; ratcheted to 25/59/19 on 2026-05-31; handler → 48 on 2026-07-20 after measuring hermetic 52.8%; **84/62/64 on 2026-08-09** after CI measured 88.4/64.3/69.1) | PASS |
| β1 | Frontend casing scanner catches camelCase | `web/scripts/check-casing.mjs` (all 18 pages pass) | PASS |
| β2 | 14 v2 page test files all pass `bun run test` | `web/src/pages/v2/**/*.test.jsx` | PASS |
| δ1 | Stage rollback script functional | `scripts/stage-rollback.sh` | PASS |
| δ2 | OpenAPI spec committed | `docs/openapi/api-v2.yaml` (1974 lines, 39 schemas) | PASS |
| ε1 | OpenAPI ↔ fixture contract gate | `scripts/check-contract.mjs` (0 mismatches, scanner verified to fire) | PASS |
| ε2 | gosec HIGH findings = 0 | `gosec -severity high -confidence medium ./internal/... ./cmd/...` (15 fixed) | PASS |

---

## 2. Operational Steps to UAT

- Configure `STAGING_KUBECONFIG` repo secret (provided manually by infra team — this is the only remaining blocker for STAGE deployment).
- Configure `E2E_BRIDGE_TOKEN` secret in `lurus-newhub-staging-secrets` (used by bridge exchange endpoint).
- Trigger staging deploy:
  ```
  gh workflow run deploy-staging.yml
  ```
- Verify pod age <5 min:
  ```
  ssh root@100.122.83.20 'kubectl -n lurus-staging get pods'
  ```
- After pod is Running: run smoke tests (Section 3).
- After Sγ (Playwright e2e) lands: trigger full e2e suite:
  ```
  gh workflow run web-ci.yml -f run_e2e=true
  ```

---

## 3. Smoke Test Golden Path

```bash
# 1. Healthcheck
curl -sf https://test-newhub.lurus.cn/api/status | jq .
# Expected: {"success":true}

# 2. Public switch endpoint (no auth required)
curl -sf https://test-newhub.lurus.cn/api/v2/switch/tools/versions | jq .
# Expected: 200 + JSON with version info

# 3. Bridge exchange (env-gated; only active when E2E_BRIDGE_TOKEN is set)
curl -sf "https://test-newhub.lurus.cn/api/v2/bridge/exchange?token=$E2E_BRIDGE_TOKEN&user_id=1" -v
# Expected: 200 + Set-Cookie header containing session cookie + JSON body with tenant_slug

# 4. Relay healthcheck via token (after minting a token via the UI)
curl -sf https://test-newhub.lurus.cn/v1/models \
  -H "Authorization: Bearer sk-<your-test-token>" | jq '.data | length'
# Expected: >0 models
```

---

## 4. Known Issues / Deferred

| Item | Reason |
|------|--------|
| ~~α7: `internal/app` coverage 19.3% (target 80%)~~ **RESOLVED 2026-08-09** | The stated reason ("not reachable hermetically — needs a real PG + Redis integration harness") was wrong. The 2026-07-25 coverage corpus reached **88.4% hermetically**, with no PG and no Redis: what was missing was tests, not infrastructure. Kept here rather than deleted because the mis-diagnosis is the lesson — "not reachable" was inferred from a low number, never tested. |
| α8: race detector on Windows dev host | GCC unavailable; race detector requires CGO. Run on Linux CI only. **2026-08-09**: this gap has now cost real defects — CI's `-race` caught a genuine production race in `internal/app/relay/helper/stream_scanner.go` (`wg.Done()` released before `SafeSendBool`, letting `close(stopChan)` interleave with the send) that no local run could see. |
| α9: coverage gate thresholds (**84/62/64**, ratcheted from 18/58/19) | Baseline thresholds, not aspirational; ratchet upward as coverage grows. 2026-05-31: app 27.0% / repo 60.9% / handler 20.0% → 25/59/19. 2026-07-20: handler hermetic re-measured at 52.8% (the "20.0%" was ~32pt stale after intervening test growth) + lifted via cover_uplift handler tests → handler gate 19→48. 2026-08-09: corpus landed (#72), CI measured app 88.4% / repo 64.3% / handler 69.1% → 84/62/64 (buffers 4.4 / 2.3 / 5.1pt). The 25 had been ~61pt below reality, i.e. the fund-handling `internal/app` code was effectively ungated. |
| Sγ: Playwright e2e suite | Deferred — depends on `STAGING_KUBECONFIG` secret being configured and STAGE pod green. |
| bun audit 404 on local host | npm audit registry unreachable from dev Windows host. Will resolve in CI (ubuntu-latest has network access to npm). |
| bun audit: 87 transitive-dep CVEs (1 crit / 26 high / 55 mod / 5 low) | Need dep-bump PR (picomatch via vitest/vite/tailwind, mermaid via @lobehub/ui, immutable, react-router, protocol-buffers-schema). CI step is informational (`continue-on-error: true`) until cleanup PR ratchets the level back up. |

---

## 5. Rollback Runbook

Rollback uses `scripts/stage-rollback.sh` (from Sδ):

```bash
# Rollback to previous image (N-1 sha)
bash scripts/stage-rollback.sh

# Forward again (restore to HEAD image)
bash scripts/stage-rollback.sh --restore
```

The script rolls `deployment/lurus-newhub` in ns `lurus-newhub` back one
revision (`kubectl rollout undo`). With the ArgoCD Application synced, pause or
delete the Application first — selfHeal reverts manual rollbacks; the durable
path is `git revert` of the auto-pin commit.

For immediate emergency rollback (no script):
```bash
ssh root@100.122.83.20 \
  "kubectl -n lurus-staging set image deployment/lurus-newhub lurus-newhub=ghcr.io/hanmahong5-arch/lurus-newhub:main-<prev-sha7>"
```

---

## 6. Contact Tree

| Role | Owner | Contact |
|------|-------|---------|
| Infrastructure / STAGING_KUBECONFIG | <TBD> | <TBD> |
| Platform gRPC (identity service) | <TBD> | <TBD> |
| On-call for STAGE incidents | <TBD> | <TBD> |
| Product sign-off | <TBD> | <TBD> |
