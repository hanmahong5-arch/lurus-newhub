# Industrial-Readiness — Owner-Gated Actions (ready-to-execute)

Companion to the 2026-06-12 readiness campaign. The CODE/manifest/CI/runbook work
is autonomous and already on the branch. The actions below touch **root/shared
manifests, platform config, or PROD/基建** — they are made *one-command ready* here
but are **NOT executed**; they wait on owner sign-off (root `CLAUDE.md` boundary).

> Sequencing reminder (from the plan's dependency order): the hardened manifests
> (P0-2 deep readiness, P1-4 spread/preStop) are **on `main` (PR #17, merged 2026-06-13)**
> but **not applied to any cluster yet** (see *Verified state* below — the staging deploy
> is skipped on an empty `STAGING_KUBECONFIG`, so there is no live target to validate against).
> Their PROD apply is blocked on **two** gates simultaneously:
>   1. **P0-5** — how PROD apply is delivered (GitOps vs manual).
>   2. **P1-2** — billing degrade live, so the deep readiness probe can't turn a
>      PG/billing blip into a fleet-wide outage. **Never** ship P0-2 to PROD ahead of P1-2.

## Verified state (2026-06-13 — live cluster + repo inspection)

Three facts were confirmed against the live cluster and the platform repo. They
**correct two assumptions the original plan made** (it assumed newhub already ran on
staging and that the backup gap was a flag flip):

1. **The Hub is not deployed to any reachable cluster.** The reachable cluster runs
   `lurus-newapi` (the open-source base) but **no `lurus-api` / `lurus-newhub`**. The
   `deploy-staging.yml` workflow **builds + pushes the image but skips the cluster apply**
   because the `STAGING_KUBECONFIG` repo secret is empty (it warns and exits 0 — a tracked
   infra gap, `doc/uat-handbook.md §2`). ⇒ the P0-2/P1-4 probe/topology changes **cannot be
   validated on any cluster** until staging is wired. New gating item: **Infra-0** below.
2. **newhub's DB (`newhub`) has ZERO backup coverage** (previously "unconfirmed"). The pg_dump
   CronJob hardcodes `-d identity`; newhub's data lives in a **separate `newhub` database**
   (schema `lurus_api`), **not inside `identity`** (live check — identity's schemas are
   app/billing/identity/module/notification/public). So no backup path touches newhub data
   today. The fix extends the dump, not a flag (P1-5).
3. **The PG is single-instance `lurus-pg-1` (no HA replica).** A node/pod loss on that one
   PG is a hard newhub outage regardless of newhub replica count — relevant to any availability SLA.

## In-cluster validation 2026-06-13 (PASS) + a deploy-blocking ns bug

The merged PR #17 image was actually deployed to R6 (`ssh root@100.98.57.55`, a disposable
PG-whitelisted ns, a self-assembled secret) and torn down clean (zero footprint). **The
hardening works on real K8s:**

- PG-only boot OK; `schema_migrations=20` (max `020_create_privacy_erasure_requests`);
  `/api/status` + `/api/health` both 200; `billing_config_poll WARN 404 keeping-current`
  (P1-2 degrade path live).
- **P0-2 proven on real kubelet probes + Service endpoints (stronger than the local stack):**
  a deny-egress NetworkPolicy cut Hub→PG → `/api/health` 503 (DB ping ~5s) → readiness 0/1 →
  pod **removed from the Service endpoints**, while `/api/status` stayed 200 → liveness held →
  **zero restarts** (no storm). Delete the netpol → 200 / Ready / back in endpoints. This is the
  exact "readiness sheds, liveness holds" contract.

**🐞 Deploy-blocking bug found:** the `staging/` + `r6-stage/` overlays and `deploy-staging.yml`
deploy to namespace **`lurus-newhub`**, which is **not** in the `database` ns `pg-access-control`
NetworkPolicy whitelist (allowed: `lurus-system, lurus-platform, lucrum, matrix, lurus-staging,
lurus-ops`). A Hub pod there gets PG **`connection refused`** and crashloops — and this also blocks
the future `STAGING_KUBECONFIG`-wired deploy. **Fix (owner picks the canonical stage ns — it
touches platform's netpol convention):**
  - **(a)** point the stage overlays + `deploy-staging.yml` at a whitelisted ns — `lurus-staging`
    is literally named for this; or
  - **(b)** have platform add `lurus-newhub` to `pg-access-control` (platform-side change).
  `lurus-newhub` is also the app / Deployment / Service **name** (keep those) — under (a) only the
  *namespace* references change (~14 sites across the two overlays + `deploy-staging.yml` + ingress).

Minor: 1 transient cold-start restart (exit 1 ~1s on first DB connect, K8s retry recovered) = the
designed PG-only fast-fail; a boot connect-retry/backoff would make first boot clean.

---

## P0-5 — Add newhub PROD to the ArgoCD ApplicationSet (`selfHeal: false` to start)

**Why gated:** edits the root governance repo's shared
`deploy/argocd/appset-services.yaml`; the template currently forces `selfHeal: true`
on every element. Auto-syncing newhub into the **intentionally-empty R1 PROD** before
PMF is exactly the owner's concern — so we start with self-heal OFF (manual sync only)
and let the owner decide when to flip it on.

**Current state:** newhub PROD is *not* in the appset (platform/newapi are). Manifest
changes are invisible to the cluster until someone manually `kubectl apply`s — which is
why this campaign's P0-2/P1-4 manifests are not yet live anywhere but staging.

### Diff (apply to ROOT repo `hanmahong5-arch/lurus`)

The template hardcodes `selfHeal: true`; to allow a per-element override, parameterize it:

```yaml
# in spec.template.spec.syncPolicy.automated:
        automated:
          prune: false
          selfHeal: {{ default true .selfHeal }}   # was: selfHeal: true
```

Then add the element (note `selfHeal: false` + explicit `revision: main`):

```yaml
          - name: lurus-newhub
            namespace: lurus-system
            repo: 2b-svc-newhub
            path: deploy/k8s
            revision: main
            selfHeal: false   # MANUAL sync only until owner approves autosync to R1
```

### Execute (owner-approved only)

```bash
# 1. Land the diff in the root governance repo on a branch, open PR, owner merges.
# 2. ArgoCD picks up the new Application (manual-sync, will show OutOfSync):
ssh root@100.98.57.55 "argocd app get lurus-newhub"
# 3. First sync is DELIBERATE and reviewed (not automatic):
ssh root@100.98.57.55 "argocd app sync lurus-newhub --dry-run"   # inspect plan
ssh root@100.98.57.55 "argocd app sync lurus-newhub"             # apply for real
# 4. Only after the hardened manifest set is verified in PROD AND P1-2 is live,
#    owner may flip selfHeal:true for drift auto-recovery.
```

**Until this is done:** apply P0-2/P1-4 manifests to staging/r6-stage manually
(autonomous), and treat PROD as un-applied. Do NOT hand-apply the deep readiness
probe to the 3-replica PROD without P1-2 (correlation-outage risk).

---

## P1-5 — newhub PG backup coverage + restore drill  ★ SLA #1 blocker

> **CORRECTED 2026-06-12 after verifying the actual artifacts** — the original
> "BACKUP_ENABLED=false ⇒ RPO=∞, just flip it" framing was an oversimplification.
> Ground truth (from `2l-svc-platform/deploy/k8s/cronjobs/pg-backup.yaml` + the live
> cluster deep-dive 2026-06-13 in the 🔴 block below — **corrected ref:** there is **no**
> `2l-svc-platform/doc/audit/2026-05-19-pg-backup-incident.md`; that path is stale, never
> committed (verified 2026-06-15: the repo has no `doc/audit/` dir). The verified
> restore-drill DR record is `2l-svc-platform/docs/runbooks/dr-drill-2026-05-20.md`):
>   - **WAL archiving is WORKING** (≈5900 WAL files, ~5 min fresh) → continuous
>     PITR stream exists; it is NOT a clean RPO=∞.
>   - **CNPG base backups are BROKEN** since ~2026-03-01 (cross-node overlay bug,
>     R2↔R1 `10.43.0.1:443`) → without a recent base, the WAL stream has nothing
>     to replay *onto*. Effective restorable RPO is therefore still unacceptable.
>   - A **pg_dump fallback CronJob** exists, gated by ConfigMap
>     `lurus-platform-backup-config` `BACKUP_ENABLED` (default off), writing to PVC
>     `lurus-pg-backup` + (weekly) MinIO bucket `pg-backups-v2`.
>   - ✅ **CONFIRMED 2026-06-13 (was "unconfirmed"):** the pg_dump CronJob hardcodes
>     `pg_dump -d identity` (file name `lurus-identity-*.dump`); newhub's data is in a
>     **separate `newhub` database** (schema `lurus_api`), **not inside `identity`** (live
>     check — identity's schemas are app/billing/identity/module/notification/public). ⇒ **no
>     backup path covers newhub's `newhub` DB today — coverage is ZERO**, not merely
>     "unconfirmed". Even flipping `BACKUP_ENABLED=true` would back up only `identity`. The
>     fix extends the dump (below).

> **🔴 LIVE DEEP-DIVE 2026-06-13 — worse than the above: the WHOLE cluster has no usable backup right now.**
>   - **The pg_dump fallback CronJob is NOT deployed** (`kubectl get cronjob -n database` → *No resources
>     found*; ConfigMap `lurus-platform-backup-config` absent; no backup PVC). The manifest lives only in
>     the platform repo — never applied. So there is **no logical-dump floor at all** today — for `identity`
>     *or* `newhub`.
>   - **CNPG base backups: `lastSuccessfulBackup=2026-03-01`, failing daily since 03-02, `lastFailedBackup`=today.**
>     With `retentionPolicy: 2d`, that one good base is **long expired → zero restorable base exists.** WAL
>     archiving still flows (last archive today; but 235k historical failures) and is **useless without a base
>     to replay onto.**
>   - **Root cause (from the failed Backup's `.status.error`):** the backup process in `lurus-pg-1` gets
>     `statusCode 500` calling `https://10.43.0.1:443/apis/postgresql.cnpg.io/...` — i.e. **the PG pod cannot
>     reach the K8s API service IP `10.43.0.1:443`** (CNI/overlay). MinIO is **healthy** (`/minio/health/live`
>     → 200), so the object store is not the problem.
>   - **Storage wrinkle:** the `local-path` StorageClass provisions under `/var/lib/rancher/k3s/storage` — on
>     the **20 GB root disk (6.3 GB free)**, NOT `/data` (98 GB, **39 GB free**). A default-SC backup PVC would
>     fill root and break the R6 disk HARD RULE → **backups must use a hostPath volume on `/data`.**
>   - **Net: `identity` + `newhub` are both effectively RPO=∞ today** — platform-wide, not newhub-only.

### Turnkey remediation (two tracks; both touch platform infra/storage → owner executes)

**Track A — immediate logical-dump floor (fast, independent of the CNPG networking bug).**
Deploy a pg_dump CronJob that dumps **every non-system** DB to **`/data`** via a node-local
hostPath volume (not the default local-path PVC, which can land on the 20 GB root):

```sh
# in the pg-dump container command, enumerate every non-system DB — covers
# identity/newapi/zitadel/lucrum/tally/… now, and `newhub` automatically once it
# deploys (a literal `-d newhub` would error today: the Hub isn't deployed yet):
DBS=$(psql -tAqc "SELECT datname FROM pg_database WHERE datistemplate=false \
      AND datname NOT IN ('postgres','template0','template1')" \
      -U postgres -h lurus-pg-rw.database.svc.cluster.local)
for DB in $DBS; do
  pg_dump -Fc -U postgres -h lurus-pg-rw.database.svc.cluster.local \
    -d "$DB" -f "/backups/lurus-${DB}-$(date -u +%Y%m%d-%H%M%S).dump"
done
# volume: hostPath /data/lurus-platform/backups + nodeSelector lurus.cn/vpn=true
#   (node-local hostPath → pin both CronJobs to R6; NOT a local-path PVC)
# then: ConfigMap lurus-platform-backup-config BACKUP_ENABLED=true
# MinIO (pg-backups-v2 @ 100.79.24.40:9000) for the weekly off-node copy.
# ⇒ IMPLEMENTED in 2l-svc-platform/deploy/k8s/cronjobs/pg-backup.yaml (the floor).
```

**Track B — durable fix (restore PITR).** Fix the PG pod → K8s API (`10.43.0.1:443`) reachability (CNI/
overlay) so the CNPG instance manager can orchestrate backups again; then bump `retentionPolicy` 2d → ≥14d
and force one base (`kubectl cnpg backup lurus-pg -n database`); verify `lastSuccessfulBackup` advances.

**Why gated:** touches platform's backup config + storage + (for the base-backup
fix) the CNPG `Cluster` CR, which is **not in any repo** — managed live via
`kubectl edit cluster lurus-pg -n database`. **Escalate earliest even though it
executes late: an unverified-coverage DB vs an enterprise SLA is non-negotiable.**

### (a) Proven 2026-06-13 — only `identity` is dumped, `newhub` is not (commands to reproduce)

```bash
# Does any backup path include the newhub DB, or only identity?
ssh root@100.98.57.55 "kubectl -n database get cm lurus-platform-backup-config -o yaml 2>/dev/null"
ssh root@100.98.57.55 "kubectl -n database get cronjob -o wide"   # daily-pg-dump target DB?
# Inspect the dump job's --dbname / -d arg in pg-backup.yaml: if it is 'identity'
# only, the newhub DB is UNPROTECTED and the CronJob must add a newhub dump.
```

### (b) Enable + cover newhub (owner)

- **Done in `2l-svc-platform/deploy/k8s/cronjobs/pg-backup.yaml` (PR, 2026-06-15):** the
  CronJob now enumerates every non-system DB at run time (no hardcoded DB list), so the
  `newhub` DB (datname **`newhub`**, owner-confirmed 2026-06-14; schema `lurus_api`) is
  covered automatically once the Hub deploys — no second dump line to hand-add later.
  Then set ConfigMap `BACKUP_ENABLED=true`. (The dump is no longer single-DB; the old
  `-d identity` hardcode is gone.)
- For full-cluster PITR: fix the CNPG base-backup overlay-network root cause
  (per the 🔴 deep-dive above: the PG pod can't reach the K8s API `10.43.0.1:443`,
  statusCode 500) and confirm `barmanObjectStore` archives resume — this is
  the durable fix; the pg_dump is the interim floor. Storage stays on R6 `/data`
  (295G SSD) / MinIO `pg-backups-v2` per the disk HARD RULE.

### (c) Restore drill — reuse the existing script (no new tooling)

`2l-svc-platform/scripts/drills/restore-drill.sh --backup-file <path>` (spins an ephemeral
`postgres:16` Docker, **never prod**; asserts row counts incl. `public.schema_migrations=20`
and writes a dated record under `doc/incidents/`) + this repo's `doc/runbook/pg-restore.md`.
The gap is a **drill record for newhub's DB**, not tooling.

```bash
# Pull the latest dump off R6, then restore into a sandbox + assert row counts.
# restore-drill.sh takes --backup-file (it does NOT auto-fetch; there is no --R6_HOST flag):
scp root@100.122.83.20:/data/lurus-platform/backups/lurus-<db>-<ts>.dump /tmp/
bash 2l-svc-platform/scripts/drills/restore-drill.sh --backup-file /tmp/lurus-<db>-<ts>.dump
# The script's asserts are identity-schema-specific (accounts/wallets/schema_migrations=20);
# for the newhub DB do a manual pg_restore + \dt / row spot-check in the same sandbox.
```

### (d) RPO/RTO target doc (fill after the drill)

| Objective | Target | Measured (drill) |
|---|---|---|
| newhub DB (`newhub`) covered by a backup? | **YES (target)** | **NO — zero coverage, verified 2026-06-13** |
| RPO (max data loss) | ≤ 6h | _TBD_ |
| RTO (time to restore) | ≤ 1h | _TBD_ |
| Drill date / operator | — | _TBD_ |
| Backup destination + retention | MinIO `pg-backups-v2`, ≥30d | _TBD_ |

**Escalate first:** RPO=∞ vs any SLA is non-negotiable — raise this for approval
before the later-sequenced execution work.

---

## Owner approval checklist (decide; code is ready)

| Item | Action | Why gated |
|---|---|---|
| **Infra-0** | Wire the `STAGING_KUBECONFIG` repo secret so the Hub actually deploys to a staging cluster | **blocks ALL probe/manifest validation** — today the deploy job skips; needs staging cluster creds |
| **Infra-1** | Fix stage-overlay namespace `lurus-newhub` → a PG-netpol-whitelisted ns (e.g. `lurus-staging`), or have platform add `lurus-newhub` to `pg-access-control` | **deploy-blocking** — PG `connection refused` → crashloop; owner picks canonical stage ns (validated 2026-06-13) |
| **P0-5** | Add newhub to ArgoCD appset, `selfHeal:false` start | edits root manifest; autosync vs empty R1 |
| **P1-2 cap** | Sign off `BILLING_DEGRADED_SPEND_CAP_LB` (default 50 LB/tenant/hr) | unsecured-spend business risk |
| **P1-5** | **🔴 WHOLE-cluster RPO=∞ (live 2026-06-13)**: pg_dump CronJob never deployed; CNPG base backups broken since 03-02 (PG pod can't reach API `10.43.0.1:443`); `retentionPolicy 2d` → no restorable base. **Track A**: deploy hostPath-`/data` pg_dump of `identity`+`newhub` (floor); **Track B**: fix pod→API + bump retention (PITR) | platform infra/storage; **SLA #1**, platform-wide |
| **P2-2** | sealed-secrets choice + decouple shared `IDENTITY_SESSION_SECRET` | controller/secret-custody choice |
| **P2-4** | `hub.lurus.cn` DNS / R1 PROD launch | owner explicitly PMF-gated |

## Deferred by red-team calibration (pre-PMF — do NOT build now)

OTel tracing activation (no real traffic to debug) · automated secret rotation
(manual kubectl suffices at this scale; only the *shared* secret is worth flagging) ·
real HPA autoscaling / aggressive resource tuning (nothing to scale) · PG full circuit
breaker (in-cluster timeout — now backed by `statement_timeout`, P1-1 — suffices) ·
DNS / R1 launch (PMF-gated, runbook-ready only).
