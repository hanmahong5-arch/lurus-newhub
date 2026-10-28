# Bifrost stage / prod flip

**Scope**: `deploy/k8s/r6-stage` = `hub.lurus.cn` (R6 production ns `lurus-newhub`). There is no separate stage cluster for newhub; UAT (`r6-uat` / `test-newhub.lurus.cn`) is the soak peer.

## Pre-flip gates (from UAT)

All must be true before trusting the r6-stage env flip:

1. UAT `RELAY_ENGINE=bifrost`, `ready=true` on Admin `GET /api/v2/admin/gateway/relay-engine`
2. At least one `lurus_gateway_relay_engine_total{engine="bifrost",modality="chat",status="ok"}` after smoke
3. Embeddings smoke when modes include `embeddings`
4. Claude `/v1/messages` smoke when modes include `messages`
5. No billing double-charge on probe traffic
6. Rollback drill understood (below)

See `doc/runbook/bifrost-uat-soak.md` Evidence / Go-no-go table.

## Manifest

```yaml
- name: RELAY_ENGINE
  value: "bifrost"
- name: RELAY_BIFROST_MODES
  value: "chat,embeddings,messages"
```

Source of truth: `deploy/k8s/r6-stage/deployment.yaml` (ArgoCD auto-sync). Never `kubectl set env`.

## Soak window after ArgoCD converges

- **Started**: 2026-10-28 after Publish `main-20261028-749d50e` / digest `sha256:0f3e9849…` (UAT smoke evidence in `bifrost-uat-soak.md`)
- **Duration**: 48–72h
- **Watch**: Admin Gateway relay-engine panel; Prometheus `lurus_gateway_relay_engine_total` / `lurus_gateway_bifrost_duration_seconds` (netdata host scrape of NodePort 30850 — nginx 404s public `/metrics`)
- **Abort if**: bifrost `status="error"` rate dominates `ok` on mapped chat **with healthy upstreams**, or settlement / wallet anomalies on probe accounts. Known noise: DeepSeek channel **402 Insufficient Balance** for `lurus-app` still increments bifrost `error` — treat as upstream wallet, not engine rollback.

## Rollback

1. Edit `deploy/k8s/r6-stage/deployment.yaml`: `RELAY_ENGINE=legacy` (or delete both RELAY_* vars)
2. Commit + push; ArgoCD selfHeal converges
3. Confirm Admin snapshot `engine=legacy` / `ready=false` (healthy default posture)

## Soft retire

After soak is green: bifrost-owned ApiTypes must not gain new features on legacy adaptors. See ADR addendum in `doc/adr/bifrost-relay-engine.md`. Physical adaptor deletion is a later campaign.
