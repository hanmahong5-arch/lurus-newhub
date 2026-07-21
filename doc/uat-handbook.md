# Wave-UAT Handbook — Lurus Newhub

**Scope**: Wave-UAT campaign (S0 + Sα + Sβ + Sδ + Sα2 + Sε). Generated 2026-05-26.

> **As-of update 2026-06-13 (Wave-2 C2):** the deploy namespace is now
> **`lurus-staging`** (was `lurus-newhub` — PG netpol, PR #20); the `-n` flags +
> the emergency-rollback `set image` below are corrected (deployment/container
> `lurus-newhub`, image `ghcr.io/hanmahong5-arch/lurus-newhub`). The
> `gh workflow run deploy-staging.yml` path (§2) is **dead** — the cluster API is
> Tailscale-only so `STAGING_KUBECONFIG` can't be wired; deploy via
> `scripts/deploy-stage.sh` (see `doc/runbook/staging-deploy.md`). The §1 matrix
> is left as the historical Sε record.

---

## 1. Test Matrix

DoD items enumerated from the Wave-UAT plan. Status as of Sε completion.

| # | DoD Criterion | Proof Artifact | Status |
|---|---------------|---------------|--------|
| α1 | Handler smoke tests green | `go test ./internal/adapter/handler/ -run TestGetAuditEvents\|TestGetGovernance\|TestListTokensV2_Pagination` | PASS |
| α2 | Repo layer ≥60% coverage | `go test -coverprofile=cov.out ./internal/adapter/repo/...` → 60.1% | PASS |
| α3 | Cross-tenant isolation tests | `internal/adapter/repo/tenant_isolation_test.go` (10 tests) | PASS |
| α4 | 5 security projection structs (view-only on list/get endpoints) | `internal/adapter/handler/{token,channel,log,billing,billing_invoice}_view.go` | PASS |
| α5 | app layer ≥80% coverage | Internal-app coverage 19.3% — target not reachable hermetically | DEFERRED (α9) |
| α6 | Race detector: `go test -race ./...` passes | Audit doc: `_bmad-output/planning-artifacts/test-debt-findings.md` | PASS (Linux CI only) |
| α7 | internal/app coverage baseline tracked | Baseline captured; gate threshold deferred to α9 | DEFERRED |
| α8 | Race audit doc committed | `_bmad-output/planning-artifacts/test-debt-findings.md` | PASS |
| α9 | Coverage gate threshold calibrated | `go-ci.yml` coverage-gate job (18/58/19 at α9; ratcheted to 25/59/19 on 2026-05-31; handler → 48 on 2026-07-20 after measuring hermetic 52.8%) | PASS |
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
| α7: `internal/app` coverage 19.3% (target 80%) | Target not reachable hermetically — needs a real PG + Redis integration harness. Deferred to a future Wave. |
| α8: race detector on Windows dev host | GCC unavailable; race detector requires CGO. Run on Linux CI only. |
| α9: coverage gate thresholds (25/59/48, ratcheted from 18/58/19) | Baseline thresholds, not aspirational; ratchet upward as coverage grows. 2026-05-31: app 27.0% / repo 60.9% / handler 20.0% → 25/59/19. 2026-07-20: handler hermetic re-measured at 52.8% (the "20.0%" was ~32pt stale after intervening test growth) + lifted via cover_uplift handler tests → handler gate 19→48. |
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

The script reads `deploy/k8s/staging/kustomization.yaml` for the current image tag and queries GHCR for the previous `main-<sha7>` tag.

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
