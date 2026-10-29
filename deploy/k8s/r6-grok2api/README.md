# r6-grok2api — Grok2API upstream for Hub relay

Self-hosted [chenyme/grok2api](https://github.com/chenyme/grok2api) on R6.
**Hub (`hub.lurus.cn`) stays the only public OpenAI-compatible API** for product
clients; this Deployment holds Grok Build/Web/Console account pools and is
reached only in-cluster.

```
product → https://hub.lurus.cn/v1/* (Hub token)
       → channel type Custom → http://grok2api.lurus-grok2api.svc:8000
       → grok2api → Grok upstream
```

## Bootstrap (once)

```bash
ssh root@100.122.83.20
mkdir -p /data/grok2api
# Write /data/grok2api/config.yaml from secret-template.yaml (replace jwt/cek/password)
kubectl apply -f deploy/k8s/r6-grok2api/namespace.yaml
kubectl -n lurus-grok2api create secret generic grok2api-config \
  --from-file=config.yaml=/data/grok2api/config.yaml
kubectl apply -k deploy/k8s/r6-grok2api/
kubectl -n lurus-grok2api rollout status deploy/grok2api
```

Then:

1. Port-forward admin UI: `kubectl -n lurus-grok2api port-forward svc/grok2api 8000:8000`
2. Sign in → import Build/Web/Console accounts → create a **Client Key** (`g2a_…`)
3. Seed Hub channel (see `doc/runbook/grok2api-upstream.md`) with that key
4. Ensure newhub netpol allows `lurus-grok2api` (already in r6-stage / r6-uat overlays)
5. SSRF: allow private IP for the Service ClusterIP (or CIDR of cluster) per runbook

## Ops notes

- Data + SQLite live on hostPath `/data/grok2api` (R6 disk rule).
- Pin `image:` to a digest after first successful pull.
- Do **not** expose NodePort/Ingress publicly — products must call Hub.
- Official xAI API remains `ChannelTypeXai=48`; this path is the unofficial
  Build/Web/Console pool via grok2api. Comply with Grok ToS on the operator side.
