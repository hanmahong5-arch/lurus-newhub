# ADR: r6-stage is the sole overlay; ArgoCD is the sole deploy convergence path

- **日期**: 2026-08-23
- **状态**: Accepted
- **上下文**: `deploy/k8s/r6-stage/deployment.yaml` sat pinned to `main@b6d96c91`
  (07-16 build) while the live cluster ran `main@d12acb24` (08-11 build) —
  i.e. **git was the stale side and the cluster was ahead**, because at that
  time nothing wrote build digests back into the manifest and the roll was done
  by hand. Discovered 2026-08-15 while registering the (manual-sync)
  `lurus-newhub-r6-stage` ArgoCD Application, whose first `OutOfSync` report was
  the only reason the drift surfaced at all. The auto-pin job
  (`bump_r6_manifest`) was added the next day, 2026-08-16 (`8ff7e42c`), which
  fixed the git-freshness half; this ADR fixes the remaining half — nothing
  applies the fresh git state to the cluster.

## 问题

Since 2026-08-16 git truth is fresh automatically (`bump_r6_manifest`), but
"someone SSHes in and applies it" is a step nobody owns and nothing reminds
anyone to do — so the drift merely changed direction (git ahead, cluster
behind) instead of going away. Meanwhile the repo
also carried a second, never-live overlay (`deploy/k8s/staging/`, Zitadel/OIDC
+ Traefik, ns `lurus-staging`) and a GitHub Actions deploy job
(`.github/workflows/deploy-staging.yml` `deploy:`, hard-disabled `if: false`)
that applied *that* overlay — a path that has never once reached a real
cluster, because the STAGE cluster API is Tailscale-only and no GitHub-hosted
runner can reach it. Two overlays for the same logical environment, one of
which was pure dead weight, made "which manifest is real" a recurring question
answered only by tribal knowledge in `deploy/k8s/r6-stage/README.md`'s
"Which overlay when" table.

## 决策

1. **`deploy/k8s/r6-stage/` is the only STAGE overlay.** `deploy/k8s/staging/`
   (Zitadel/OIDC + Traefik, ns `lurus-staging`) is deleted outright — it never
   had a live footprint (`deploy/k8s/r6-stage/README.md`'s 2026-08-15 resource
   inventory already confirmed ns `lurus-newhub` is the only namespace with
   real objects), so there is no cutover, only cleanup. The OIDC-vs-
   platform-identity question this overlay represented was already resolved
   in favor of platform-identity when r6-stage went live; this ADR just
   removes the artifact that kept re-raising it.
2. **`.github/workflows/deploy-staging.yml` is deleted outright**, not kept
   "for its warning" as the previous runbook prescribed. The ADR itself is now
   the durable record of the infra gap (Tailscale-only cluster API); a
   hard-disabled CI job that builds an image (`:staging` tag) with zero
   consumers is not doing documentation work that this file doesn't already do
   better, and it was the sole consumer keeping `deploy/k8s/staging/` alive.
3. **ArgoCD becomes the primary convergence path.** `deploy/k8s/argocd/application.yaml`
   registers an automated + selfHeal Application tracking `deploy/k8s/r6-stage`
   on `main`. This closes the gap directly: no human step between "CI auto-pins
   a digest" and "cluster runs it". `prune` stays off (see that file's
   comments) — the overlay does not enumerate the hand-applied Secret, so
   pruning would delete it.
4. **`scripts/deploy-stage.sh` (SSH) becomes the fallback**, used when ArgoCD
   is unreachable/misconfigured or for secret rotation (which ArgoCD
   deliberately does not manage). Gained a `SKIP_SECRETS=1` mode for routine
   fallback redeploys that don't need to touch secrets.

This supersedes the manual-sync `lurus-newhub-r6-stage` Application
registered 2026-08-15 (`deploy/k8s/r6-stage/argocd-application.yaml`) — that
Application was deliberately manual only because no trustworthy
digest-bump-on-build step existed at registration time. That step landed one
day later (`8ff7e42c`, 2026-08-16) and has auto-pinned every `main` build
since, so the caveat is now obsolete. Because that caveat's real fear was
"automated sync rolls the cluster BACK to a stale git digest", the cutover in
`deploy/k8s/argocd/README.md` requires diffing the rendered overlay against the
live cluster **before** `kubectl apply`ing the Application — creating an
`automated` Application starts syncing immediately, there is no dry-run window.
Per `deploy/k8s/argocd/README.md`, the operator deletes the old Application
when wiring up the new one so exactly one Application ever owns the
namespace's resources.

## 回滚

- **Stop using ArgoCD**: `kubectl -n argocd delete application lurus-newhub --cascade=orphan`.
  Cascade=orphan touches only the Application object; the Deployment/Service
  are untouched (prune was off the whole time). Deploys revert to
  `scripts/deploy-stage.sh` runs, which is unaffected by this ADR — it still
  applies the same `deploy/k8s/r6-stage` overlay it always has.
- **Restore the deleted overlay/workflow**: both are plain deletions in git
  history (`git log --diff-filter=D -- deploy/k8s/staging .github/workflows/deploy-staging.yml`);
  `git revert`/`git checkout <sha>^ -- <path>` recovers them verbatim if the
  OIDC/Traefik path is ever revived as a real product decision rather than
  dead weight.

## 残留缺口（诚实边界）

- **The Application has not been applied to any cluster by this change** — it is
  a manifest in git plus a runbook. Nothing here was verified against live
  ArgoCD; `Synced/Healthy` remains unproven until an operator runs the cutover.
- ArgoCD repo credentials are **not needed**: the repo is public (verified
  2026-08-23 — `gh repo view --json isPrivate` → `false`, and an
  unauthenticated `info/refs?service=git-upload-pack` GET returns `200`, which
  is the exact fetch ArgoCD performs). An earlier draft of this ADR claimed the
  repo was private and prescribed a deploy-key step; that was wrong and is
  removed, because a wrong `repository` Secret matching this repoURL would
  break an otherwise-working anonymous sync.
- **`scripts/stage-rollback.sh` is broken independently of this change**: it
  hardcodes `readonly NAMESPACE="lurus-staging"` with no env override, a
  namespace the live cluster does not use (`deploy/k8s/r6-stage/README.md`
  2026-07-07 revert; `doc/runbook/seam-s1-activation.md` live-verified
  2026-08-22 — the workload is in ns `lurus-newhub`). Any run of it fails at
  `kubectl rollout undo`. `doc/runbook/staging-deploy.md` therefore spells the
  rollback out inline instead of calling that script.
- Several docs outside this change's file scope still reference the deleted
  `deploy/k8s/staging/` overlay / `:staging` tag / `deploy-staging.yml`
  (`doc/runbook/staging-environment.md`, `doc/uat-handbook.md`,
  `doc/runbook/industrial-readiness-gated-actions.md`,
  `doc/phase-d-r6-smoke-checklist.md`, `doc/runbook/seam-s1-activation.md`,
  `scripts/stage-rollback.sh`, `deploy/k8s/r6-stage/README.md`'s "Which
  overlay when" table, and `_bmad-output/` planning artifacts). Follow-up
  needed to reconcile those; out of scope here.
