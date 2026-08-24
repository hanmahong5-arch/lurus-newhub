# lurus-newhub — R6 STAGE manifest

Live deployment of `2b-svc-newhub` on the R6 STAGE cluster, fronted by R6 host nginx.

- Cluster: R6 (`43.226.45.87`, Tailscale `100.122.83.20`), single-node K3s — per `lurus/CLAUDE.md` Server Landing SSOT
- Namespace: `lurus-newhub` — the ns the live Deployment/Service actually run in. (An earlier revision moved this to `lurus-staging` believing PG's `pg-access-control` netpol required it; that netpol no longer exists — `kubectl get netpol -n database` is empty — so the manifests were reverted to `lurus-newhub` to match the live cluster, 2026-07-07.)
- Domain: https://test-newhub.lurus.cn
- Service: NodePort 30850 -> container port 3000
- Image: `ghcr.io/hanmahong5-arch/lurus-newhub@sha256:…` (pinned digest, `imagePullPolicy: IfNotPresent`). The pin is **auto-written by CI**: after every gate-passing `:main` build, the `bump_r6_manifest` job in `docker-image-main.yml` rewrites `deployment.yaml`'s `# pin:`/`image:` lines to the pushed digest (`[skip ci]` commit). Roll forward = just sync the ArgoCD app / `apply -k` — never `kubectl set image` (that recreates the manifest-behind-cluster drift closed on 2026-08-15).
- GitOps: ArgoCD Application `lurus-newhub` (ns `argocd`, manifest in
  `deploy/k8s/argocd/`) tracks this directory with **automated** sync
  (prune off, selfHeal on): every auto-pin commit on main converges the
  cluster within the sync interval. See `deploy/k8s/argocd/README.md` for
  wiring, rollback and the selfHeal-vs-manual-kubectl warning.

## This is the only overlay

The sibling `deploy/k8s/staging/` overlay (Traefik + Zitadel-era OIDC, ns
`lurus-staging`, `:staging` tag) was deleted on 2026-08-23 — it never had a
cluster footprint and its auth mode was superseded by platform-identity. The
decision record is `doc/decisions/2026-08-23-deploy-canonical-r6-stage.md`.

## Resource inventory (live-verified 2026-08-15)

`kubectl -n lurus-newhub get deploy,svc,cm,ingress,hpa,pdb` on R6 returns exactly:
Deployment `lurus-newhub` (rev 38, 3/3 ready) + Service `lurus-newhub` (NodePort) +
the auto-generated `kube-root-ca.crt` ConfigMap every namespace gets for free.
**No Ingress, HPA, or PodDisruptionBudget objects exist in this namespace** — there
is nothing to export for those kinds; do not add empty placeholder manifests for
them. Ingress-equivalent routing is done by R6 host nginx (`deploy/r6-host-nginx/`),
not a K8s Ingress object.

Secret `lurus-newhub-secrets` carries exactly 6 keys (verified via
`kubectl get secret lurus-newhub-secrets -n lurus-newhub -o go-template` printing
key names only, values never read/stored — see `secret-template.yaml`).

`deployment.yaml` / `service.yaml` in this directory were diffed field-by-field
(minus `status`/`metadata.{uid,resourceVersion,creationTimestamp,generation}`/
`kubectl.kubernetes.io/last-applied-configuration`) against the live objects on
2026-08-15 and match exactly — no drift.

### Relationship to the docker-compose `lurus-api` instance

This K8s Deployment is unrelated to the `lurus-api` container defined by the
root `docker-compose.yml` / `lurus-api.service`. That compose instance is a
**frozen legacy artifact** (up 2+ months on R6 outside this namespace, untouched
since; do not `docker-compose up`/restart/edit it as part of any K8s work here).
The live product traffic (`test-newhub.lurus.cn`) is served exclusively by the
`lurus-newhub` Deployment in this directory.

## First apply

```bash
# 1. Seed the secret with real values (NEVER commit them; see secret-template.yaml
#    for the full key list incl. optional TAVILY_API_KEY):
kubectl -n lurus-newhub create secret generic lurus-newhub-secrets \
  --from-literal=SESSION_SECRET='<real>' \
  --from-literal=SQL_DSN='<real>' \
  --from-literal=IDENTITY_SERVICE_INTERNAL_KEY='<real>' \
  --from-literal=IDENTITY_SESSION_SECRET='<real>' \
  --from-literal=LURUS_WHITELABEL_MASTER_SECRET='<real>' \
  --from-literal=TAVILY_API_KEY='<real, optional>'

# 2. Apply the rest. secret-template.yaml is intentionally NOT in the kustomize
#    resources (see kustomization.yaml): kubectl apply REPLACES Secret data
#    per-key rather than merging, so rendering the placeholder template would
#    clobber the real values seeded above. `apply -k` leaves the Secret alone.
kubectl apply -k deploy/k8s/r6-stage/

# 3. (Optional/legacy) Seed the default tenant (slug='lurus') by hand — migration
#    021 §4 now self-seeds this idempotently on boot (resolves by id='default' OR
#    slug='lurus', creates only if truly absent), so this manual step is no longer
#    required for a fresh deploy. Keep it only for pre-021 bootstrap or recovery;
#    if run, the script now uses the canonical id='default' (021's compat branch
#    still tolerates a legacy id='lurus-default' row from old STAGE data, but new
#    seeds must not create more of it).
#    db name is `newhub` (owner-confirmed 2026-06-14). The tables live in the
#    `public` schema — measured 2026-08-24, 40 tables; the `lurus_api` schema the
#    service CLAUDE.md used to name does not exist and that claim is now removed.
#    The PG pod is lurus-pg-0 in ns `database` (StatefulSet lurus-pg, 1 replica —
#    re-verified 2026-08-24; the earlier "lurus-pg-1" here was wrong).
ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-0 -- \
  psql -U lurus -d newhub" < deploy/k8s/r6-stage/seed-default-tenant.sql
# (PG pod is lurus-pg-0 in ns database — live-verified 2026-07-07; a stale ref to
#  lurus-pg-1 was corrected. seed-default-tenant.sql already targets lurus-pg-0.)
```

## Sync nginx vhost to R6 host

```bash
scp deploy/r6-host-nginx/test-newhub.conf root@100.122.83.20:/etc/nginx/sites-available/test-newhub
ssh root@100.122.83.20 "ln -sf ../sites-available/test-newhub /etc/nginx/sites-enabled/ && nginx -t && systemctl reload nginx"
```

> Promote to PROD only after pinning the image to `main-<sha7>`.
