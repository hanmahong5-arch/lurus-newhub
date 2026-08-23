# ArgoCD wiring for lurus-newhub (R6 STAGE)

Turns "merge to main → CI auto-pins the digest → **someone remembers to SSH-apply
it**" into "merge to main → CI auto-pins the digest → ArgoCD notices the git
change and converges the cluster within its poll interval, no human required."
See `doc/decisions/2026-08-23-deploy-canonical-r6-stage.md` for why (12 days /
74 commits sat un-deployed before this existed).

`application.yaml` in this directory registers Application `lurus-newhub`
(automated sync, `prune: false`, `selfHeal: true`) tracking
`deploy/k8s/r6-stage` on `main`. Read the comments at the top of
`application.yaml` before touching the sync policy — `prune: false` is load
bearing (protects the hand-applied `lurus-newhub-secrets` Secret and any other
untracked resource in the namespace).

The R6 cluster already runs ArgoCD (ns `argocd`, verified 7/7 pods Running) —
there is nothing to install. This repo (`hanmahong5-arch/lurus-newhub`) is
**public**, so ArgoCD needs **no repo credentials at all**:

```bash
gh repo view hanmahong5-arch/lurus-newhub --json isPrivate,visibility
# {"isPrivate":false,"visibility":"PUBLIC"}                      (2026-08-23)
curl -s -o /dev/null -w '%{http_code}\n' \
  'https://github.com/hanmahong5-arch/lurus-newhub.git/info/refs?service=git-upload-pack'
# 200  ← unauthenticated; this is exactly the fetch ArgoCD performs
```

Do **not** create a `repository` Secret for this repoURL. A credential whose
`url` matches `spec.source.repoURL` but whose auth material is wrong (e.g. an
`sshPrivateKey` paired with an `https://` URL) turns a sync that works
anonymously into `ComparisonError`. Only if the repo is ever flipped to private
does the credential step become necessary — then, and only then, register a
read-only deploy key as a `Secret` in ns `argocd` labelled
`argocd.argoproj.io/secret-type=repository`, with `url` byte-identical to
`spec.source.repoURL` and the scheme (ssh vs https+PAT) matching it.

> **selfHeal changes how you deploy by hand.** The service `CLAUDE.md` still
> documents `kubectl set image deployment/lurus-newhub ... -n lurus-newhub` as
> the deploy step. Once this Application is active that command is *temporary*:
> selfHeal reverts the live Deployment to the digest pinned in
> `deploy/k8s/r6-stage/deployment.yaml` on `main` at the next reconcile. That is
> the intended behaviour (git is the source of truth), but it means a manual
> image swap — including a manual rollback — needs either a git change or
> `kubectl -n argocd patch application lurus-newhub --type merge -p
> '{"spec":{"syncPolicy":null}}'` first.

## 0. Retire the old manual-sync Application first

`deploy/k8s/r6-stage/argocd-application.yaml` already registers a **different**
Application, `lurus-newhub-r6-stage` (manual sync, no prune, no selfHeal,
registered 2026-08-15 back when there was no trustworthy digest-bump-on-build
step yet). That step (`bump_r6_manifest` — the auto-pin job in
`.github/workflows/docker-image-main.yml`) landed 2026-08-16 in commit
`8ff7e42c`, one day after that registration; it is now the reliable source of
git freshness that made the manual-sync caveat obsolete.

Two Applications targeting the same destination namespace will both claim
ownership of the same live resources (ArgoCD's "shared resource" warning) and
can fight if their sync policies disagree. Delete the old one **before**
applying the new one:

```bash
kubectl -n argocd delete application lurus-newhub-r6-stage --cascade=orphan
# --cascade=orphan: removes only the Application object. It never had
# automated sync, so it never owned/mutated the live Deployment/Service —
# nothing to orphan in practice, this flag is just the safe default.
```

## 1. (No credential step — public repo)

Nothing to do; see the anonymous-fetch check at the top of this file.

## 2. Pre-flight diff — MUST run BEFORE step 3

An `automated` Application starts reconciling as soon as it is created; there
is no dry-run window. If the live cluster is running a *newer* image than the
digest pinned on `main` (this is exactly what happened on 2026-08-15: git on a
07-16 digest, cluster on an 08-11 one), applying the Application **rolls STAGE
backwards** on its first sync. Check first, on the R6 host:

```bash
kubectl kustomize deploy/k8s/r6-stage | kubectl diff -f - || true
# `kubectl diff` exits 1 on any delta — read the diff, don't just check $?.
# Churny deltas (resourceVersion, managedFields) are fine. What must NOT be
# there: a container `image:` line where the live digest differs from the
# rendered one in a direction you did not intend.
```

Cross-check the digests explicitly:

```bash
grep -A1 '# pin:' deploy/k8s/r6-stage/deployment.yaml    # what git will apply
kubectl -n lurus-newhub get deploy lurus-newhub \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'   # what runs now
```

If they differ and the live one is newer, land the correct digest in `main`
first (re-run `bump_r6_manifest`, or push the pin) and only then continue.

## 3. Apply the Application

```bash
kubectl apply -f deploy/k8s/argocd/application.yaml
```

## 4. First-sync verification (do this before walking away)

```bash
argocd app get lurus-newhub                 # expect Synced, Healthy
# or without the argocd CLI:
kubectl -n argocd get application lurus-newhub -o jsonpath='{.status.sync.status} {.status.health.status}{"\n"}'

# The image actually running must now equal the digest pinned in git:
kubectl -n lurus-newhub get deploy lurus-newhub \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
curl -fsS https://test-newhub.lurus.cn/api/health | jq .

# The hand-applied Secret must still exist (prune is off, so it should — this
# is the check that proves it):
kubectl -n lurus-newhub get secret lurus-newhub-secrets
```

## Rollback (ArgoCD → back to hand-SSH-apply mode)

```bash
kubectl -n argocd delete application lurus-newhub --cascade=orphan
# cascade=orphan: deletes the Application object only. The Deployment/Service
# in ns lurus-newhub are left exactly as they were — ArgoCD only ever applied
# what scripts/deploy-stage.sh would have applied anyway (same overlay), and
# prune was off the whole time, so there is no cleanup to reconcile.
```

After this, `scripts/deploy-stage.sh` (SSH path) is once again the only
convergence mechanism — resume manual/CI-triggered runs of it per
`doc/runbook/staging-deploy.md`.
