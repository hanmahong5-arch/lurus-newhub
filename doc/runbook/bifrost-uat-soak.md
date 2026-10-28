# Bifrost UAT soak

**Status**: armed on `r6-uat` **and** `r6-stage`/prod (`RELAY_ENGINE=bifrost`, modes `chat,embeddings,messages`). Soft-retire of owned legacy adaptors: `doc/adr/bifrost-relay-engine.md`.

## Evidence (2026-10-28 post-merge smoke — `#264` / `main-20261028-749d50e`)

| Check | Result |
|-------|--------|
| Image | `ghcr.io/hanmahong5-arch/lurus-newhub@sha256:0f3e9849e5713a1a6547b7f1fe156bd5d592205eef33eb33bbd2efd5006943f9` (UAT + prod digest match) |
| Env | `RELAY_ENGINE=bifrost`, `RELAY_BIFROST_MODES=chat,embeddings,messages` on `ns/lurus-newhub-uat` **and** `ns/lurus-newhub` |
| Pod | UAT `lurus-newhub-6c7ccfd996-fmxdx` 1/1; prod 3× `598858d45c-*` 1/1 |
| Admin `GET /api/v2/admin/gateway/relay-engine` | `engine=bifrost`, `modes=[chat,embeddings,messages]`, `ready=true` |
| Chat smoke | faultsim channel `bifrost-soak-faultsim` → `uat-bifrost-chat`→`ok-chat`: HTTP 200; metric `engine=bifrost,modality=chat,status=ok` ≥ 1 |
| Embeddings smoke | same channel `uat-bifrost-embed`→`ok-embed`: HTTP 200; `modality=embeddings,status=ok` ≥ 1 |
| Messages smoke | `POST /v1/messages` same model: HTTP 200 Claude wire; `modality=messages,status=ok` ≥ 1 |
| Billing | one consume log each path; chat/messages `quota=450` for 1000/500 @ ratio 0.15/4 (no double PostConsume) |
| Prod traffic note | live `/v1/chat/completions` from `lurus-app` mostly upstream **402 Insufficient Balance** on DeepSeek channels — counted as bifrost `status=error`. Engine path is live; not a bifrost Init failure. |

### Earlier same-day re-check (pre-smoke)

| Check | Result |
|-------|--------|
| Image | `sha256:64dbeb78…` (superseded by pin above) |
| Chat path | CONDITIONAL — no ok counter until faultsim probe |

### Prior evidence (2026-10-27)

| Check | Result |
|-------|--------|
| Image | `main-20261027-3c34758` / `sha256:381bb60e…` |
| Boot log | `relay engine: bifrost (modes=chat)` |
| Metrics | empty until first chat |

## Go / no-go

| Gate | Pass criteria | 2026-10-28 post-smoke |
|------|---------------|------------------------|
| Engine armed | `RELAY_ENGINE=bifrost`, pod Ready, `/api/status` 200 | **PASS** |
| Chat path | `relay_engine_total{engine="bifrost",modality="chat",status="ok"}` ≥ 1 | **PASS** |
| Embeddings | `modality=embeddings,status=ok` ≥ 1 | **PASS** |
| Messages | `modality=messages,status=ok` ≥ 1 | **PASS** |
| Billing | Probe usage log rows ≈ single PostConsume (no double) | **PASS** (450 + 1 + 450) |
| Legacy fallthrough | Unmapped/pass-through still increments `engine="legacy"` | unchanged contract |

**Verdict**: **GO** for 48–72h soak watch on prod (`doc/runbook/bifrost-stage-prod-flip.md`). Abort only if bifrost `error` dominates `ok` on *healthy* upstreams (ignore known upstream 402 balance exhaustion).

## Check after ArgoCD converges

```bash
# Relay engine snapshot (root session cookie)
curl -sS -b "$COOKIE" https://test-newhub.lurus.cn/api/v2/admin/gateway/relay-engine
# Expect engine=bifrost, modes includes chat,embeddings,messages, ready=true

# Metrics: scrape pod IP or NodePort direct (NOT https://test-newhub…/metrics — nginx 404)
kubectl -n lurus-newhub-uat get pod -l app=lurus-newhub -o wide
# curl http://<podIP>:3000/metrics | grep lurus_gateway_relay_engine_total
```

Smoke:

1. OpenAI-compatible chat through a mapped channel → `engine="bifrost"`.
2. Pass-through / unmapped apiType → `engine="legacy"`.
3. `POST /v1/embeddings` on a mapped OpenAI-family channel → `modality="embeddings"`.
4. `POST /v1/messages` (Claude wire) → `modality="messages"`.

## Rollback

Set `RELAY_ENGINE=legacy` (or remove the two env vars) in `deploy/k8s/r6-uat/deployment.yaml` and let ArgoCD heal.

## Stage / prod

See `doc/runbook/bifrost-stage-prod-flip.md`. Soak window 48–72h after this evidence pack.
