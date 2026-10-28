# Bifrost UAT soak

**Status**: armed on `r6-uat` (`RELAY_ENGINE=bifrost`). Stage/prod flips follow Wave 3/4 in the fusion roadmap (`doc/adr/bifrost-relay-engine.md` + soft-retire addendum).

## Evidence (2026-10-28 live re-check)

| Check | Result |
|-------|--------|
| Image | `ghcr.io/hanmahong5-arch/lurus-newhub@sha256:64dbeb7823ced22bed32cf98a894b8d7484c8d4685c904282ca1a65d91f7aa1a` |
| Env | `RELAY_ENGINE=bifrost`, `RELAY_BIFROST_MODES=chat,embeddings,messages` on `ns/lurus-newhub-uat` (manifest; ArgoCD may lag) |
| Pod | `lurus-newhub-785bc579b5-sv4mp` Running 1/1 (peer pod ContainerStatusUnknown — ignore for soak) |
| `/api/status` | 200 via NodePort 30851 |
| `/metrics` | Host nginx returns 404 for `/metrics` (by design). Netdata scrapes pod/NodePort direct; series `lurus_gateway_relay_engine_total` only appears after first chat dispatch |
| Boot log | Process past Init (health/status serving); bifrost Init is silent beyond `relay engine: bifrost` at cold start (rotated out of 24h tail on long-lived pods) |

### Prior evidence (2026-10-27)

| Check | Result |
|-------|--------|
| Image | `main-20261027-3c34758` / `sha256:381bb60e…` |
| Boot log | `relay engine: bifrost (modes=chat)` |
| Metrics | empty until first chat |

## Go / no-go (Wave 1 → Wave 2)

| Gate | Pass criteria | 2026-10-28 |
|------|---------------|------------|
| Engine armed | `RELAY_ENGINE=bifrost`, pod Ready, `/api/status` 200 | **PASS** |
| Chat path | At least one `lurus_gateway_relay_engine_total{engine="bifrost",modality="chat",status="ok"}` after smoke, or Admin `relay-engine` totals > 0 | **CONDITIONAL** — no chat traffic on UAT since last restart; smoke required before Wave 3 |
| Legacy fallthrough | Unmapped/pass-through still increments `engine="legacy"` | unchanged contract |
| Billing | Probe chat: usage log rows ≈ wallet/settlement events (no double PostConsume) | smoke when chat probe runs |
| Embeddings widen | After `RELAY_BIFROST_MODES=chat,embeddings`, one OpenAI embeddings call on mapped channel → `engine=bifrost,modality=embeddings` | pending ArgoCD converge |

**Wave 1 verdict**: **GO** to enable embeddings on UAT and continue Wave 2 code. **Do not** flip stage (Wave 3) until chat smoke produces a bifrost `ok` counter on UAT.

## Check after ArgoCD converges

```bash
# Relay engine snapshot (root session cookie)
curl -sS -b "$COOKIE" https://test-newhub.lurus.cn/api/v2/admin/gateway/relay-engine
# Expect engine=bifrost, modes includes chat (+ embeddings after Wave 1), ready=true

# Metrics: scrape pod IP or NodePort direct (NOT https://test-newhub…/metrics — nginx 404)
kubectl -n lurus-newhub-uat get pod -l app=lurus-newhub -o wide
# curl http://<podIP>:3000/metrics | grep lurus_gateway_relay_engine_total
```

Smoke:

1. OpenAI-compatible chat through a mapped channel → `engine="bifrost"`.
2. Pass-through / unmapped apiType → `engine="legacy"`.
3. After embeddings mode: `POST /v1/embeddings` on a mapped OpenAI-family channel → `modality="embeddings"`.

## Rollback

Set `RELAY_ENGINE=legacy` (or remove the two env vars) in `deploy/k8s/r6-uat/deployment.yaml` and let ArgoCD heal.

## Stage / prod (later waves)

See `doc/runbook/bifrost-stage-prod-flip.md` (Wave 3/4). Soak window 48–72h on stage before prod.
