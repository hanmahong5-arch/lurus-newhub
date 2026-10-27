# Bifrost UAT soak

**Status**: env armed on `r6-uat` (`RELAY_ENGINE=bifrost`, `RELAY_BIFROST_MODES=chat`). Production stays `legacy`.

## Check after ArgoCD converges

```bash
# Relay engine snapshot (root session)
curl -sS -b "$COOKIE" https://test-newhub.lurus.cn/api/v2/admin/gateway/relay-engine

# Expect engine=bifrost, modes includes chat, ready=true once Init succeeds.
# Metrics (host netdata scrape / pod direct):
#   lurus_gateway_relay_engine_total{engine="bifrost|legacy",modality="chat",status="..."}
```

Smoke: one OpenAI-compatible chat through a mapped channel; pass-through / unmapped apiTypes must still land on legacy (counter `engine="legacy"`).

## Rollback

Set `RELAY_ENGINE=legacy` (or remove the two env vars) in `deploy/k8s/r6-uat/deployment.yaml` and let ArgoCD heal. Do not flip stage/prod defaults in this soak.
