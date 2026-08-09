# ADR: HA DB-Lease Leader Election + Leader-Gated Token Auto-Rotation

**Status**: Accepted (2026-05-29) — implementation landed; STAGE drills pending
**Date**: 2026-05-29
**Authors**: Engineering (lurus-newhub)
**Affected service**: `2b-svc-newhub` (hub.lurus.cn, stage test-newhub.lurus.cn)
**Horizon**: H1.3 (F1 HA multi-master + failover) and H1.4 (F4 secret auto-rotation)

---

## 1. Context & Problem Statement

Newhub gated its master-only background tasks (OpenRouter sync + aggregator,
pool reaper, Midjourney/Task pollers, audit retention cleanup, boot-time DB
migration) on a static `NODE_TYPE` env var: `IsMasterNode = NODE_TYPE != "slave"`.
With a single replica this works, but it provides no HA: if the single master
dies, every master-only task stops until a human intervenes, and scaling to
multiple replicas would make all of them "master" and run each task N times
(duplicate work + concurrent-migration races on PostgreSQL).

Separately, API tokens never rotate automatically. Long-lived keys are a
standing credential-hygiene risk for enterprise tenants; the bar to clear for
H1 ("可投标") includes scheduled secret rotation.

`client-go` is **not** a dependency (verified via go.mod), so a Kubernetes
`Lease`-based election was rejected to avoid pulling in the k8s client + RBAC.
A DB-lease is dev/prod-identical (works on SQLite and PostgreSQL), needs no
ServiceAccount, and reuses infrastructure already present.

## 2. Decision

**H1.3** — Add a single-row DB lease (`leader_elections`, primary key
`name='master'`). A process is the leader while it owns the row and
`now < expires_at`. Leadership is layered on the existing tier rather than
replacing it:

- `NODE_TYPE=slave` → does not participate, never leader (unchanged behavior).
- `NODE_TYPE` unset/`master` → participates; among master-capable replicas
  exactly one wins the lease. `common.IsLeader()` is the single source of truth.

Each master-only loop runs on all master-capable replicas but gates its
**per-tick** work on `common.IsLeader()`, so exactly one replica does the work
and a dead leader is replaced within the lease TTL (30s; renew every ~10s).
The boot-time DB migration is gated on a boot lease so only one replica runs
AutoMigrate.

**H1.4** — Add `tokens.auto_rotate_days` (0 = disabled) and `tokens.rotated_at`.
A leader-gated daily task (`lifecycle.StartSecretRotationWithContext` wrapping a
`LeaderTask`) rotates every due token's key, stamps `rotated_at`, records an
`auth.token_rotated` audit event, and emails the owner. The rotation decision
is the pure, clock-injectable `app.NeedsRotationAt(autoRotateDays, rotatedAt, now)`.

## 3. Schema

```sql
-- 018
CREATE TABLE leader_elections (
    name        VARCHAR(64)  PRIMARY KEY,
    holder_id   VARCHAR(128) NOT NULL DEFAULT '',
    acquired_at BIGINT       NOT NULL DEFAULT 0,
    renewed_at  BIGINT       NOT NULL DEFAULT 0,
    expires_at  BIGINT       NOT NULL DEFAULT 0
);
-- 017
ALTER TABLE tokens ADD COLUMN auto_rotate_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tokens ADD COLUMN rotated_at        BIGINT  NOT NULL DEFAULT 0;
```

GORM AutoMigrate drives both from the structs; the SQL files are the
out-of-band PostgreSQL counterparts. Migration ledger IDs 017 + 018 reserved.

## 4. Election Semantics (repo.TryAcquireOrRenew)

A single conditional UPDATE wins iff `holder_id = me OR expires_at < now`; if it
affects 0 rows the first-acquire INSERT runs under the name primary key, so a
racing loser gets a duplicate-key error reported as "not leader". The DB
serializes competing writers → **at any instant at most one holder owns a
non-expired lease**. `now` is injected for deterministic unit tests
(acquire / renew / reject-while-valid / takeover-after-expiry /
two-candidates-one-wins / release). On graceful shutdown the leader calls
`ReleaseLease` (sets `expires_at = 0`) so a standby takes over immediately
rather than waiting out the TTL. On transient DB error during renewal the
manager keeps leadership until its local lease estimate lapses, avoiding
needless failover churn.

## 5. Rollback / Remediation (H1.4)

Each `auth.token_rotated` audit event's `details` carries
`{"reason":"auto_rotate","auto_rotate_days":N,"old_key_prefix":"sk-xxxx","old_expired_time":T}`.
This is **identification + remediation info, not a literal key restore**: the
old secret is intentionally NOT retained (retaining it would defeat rotation).
Within the audit-retention window an operator can correlate the rotated token
by prefix, see its prior expiry, and re-issue / communicate as needed. There is
no automatic "un-rotate"; that is a deliberate security choice.

## 6. Known Tradeoff — multi-replica cold start

> **SUPERSEDED 2026-07-14 by `57e22c8a`.** Lease-gated migrations turned out to
> fail in the opposite direction: the surviving leader holds the lease for its
> whole lifetime, so on every *rolling update* the new pods lost the lease and
> skipped migrations entirely — 2026-07-15 prod sat at schema 022 while the
> shipped code expected 026. Boot migrations now run on **every** master-capable
> replica (`repo.runBootMigrations`, called outside the `if gotLease` branch),
> serialized by `bootAutoMigrateLockID` for AutoMigrate plus the runner's own
> `migration.AdvisoryLockID`; both phases are idempotent no-ops when nothing is
> pending. The concurrent-AutoMigrate race this section cites is what that first
> advisory lock exists to prevent. The replicas=1-first rollout mitigation below
> is no longer needed.

On a **first-ever** cold start of a fresh multi-replica DB, only the boot-lease
winner runs migrations; the losers skip (mirroring the prior slave-skips-
migration behavior) and may briefly serve before tables exist. Mitigation:
roll out the first deploy at replicas=1 (or run a one-shot migration) before
scaling to 3. On subsequent boots the tables already exist, so this window does
not recur. AutoMigrate-on-every-master was rejected because concurrent
AutoMigrate races on PostgreSQL (relation-already-exists / deadlocks).

## 7. Manifests

`deploy/k8s/hpa.yaml` `minReplicas` 1→3 (always a standby pool) and
`deploy/k8s/r6-stage/deployment.yaml` `replicas` 1→3 so the failover drill is
possible on STAGE. podAntiAffinity is already `preferred` in the base
deployment. No ServiceAccount/RBAC required.

## 8. Verification

- **Local (done)**: `go test ./internal/...` green; production build green.
  Election semantics + LeaderTask gating + rotation (due/not-due/disabled,
  owner email, audit event) covered by clock-injected SQLite unit tests.
- **STAGE-gated (NOT faked locally)**:
  1. `kubectl delete pod <leader>` → a standby's `IsLeader()` flips true and
     master-only tasks resume within the TTL with no gaps.
  2. Real SMTP delivery of the rotation notice to a token owner.

## 9. Alternatives Rejected

- **Kubernetes Lease (client-go)**: adds k8s client + RBAC; not dev/prod
  identical; `client-go` absent from go.mod.
- **Per-task leases**: over-engineered; one lease guards all master-only work.
- **Keep static NODE_TYPE**: no failover; multi-replica duplicates work.
- **Postgres advisory lock for migrations**: PG-only (breaks SQLite dev parity).
  > **SUPERSEDED 2026-07-14.** The SQLite dev tier was removed in 2026-06
  > (`SQL_DSN` must be `postgres://` or boot fast-fails; glebarez SQLite survives
  > only in the hermetic unit-test tier), which removed the parity objection —
  > and that is exactly what unblocked `57e22c8a`. Advisory locks are now the
  > mechanism: `bootAutoMigrateLockID` around AutoMigrate and
  > `migration.AdvisoryLockID` inside the runner.
