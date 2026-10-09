# Third-party notices

This file records where code or design in this repository was taken from other
open-source projects, per delivery lane. Upstream provenance of the base
project (New API, One API) is in `NOTICE`. Every source file that carries
adapted material names its origin in its header comment.

Legend: "adapted" = behaviour, endpoint contracts or wording follow the
original and the Go code was rewritten for this repository; "design only" = an
idea was reimplemented from scratch and no source or wording was copied.

## Plan-account quota windows (lane B1, `internal/app/planquota`)

Adapted from Wei-Shaw/sub2api (LGPL-3.0), https://github.com/Wei-Shaw/sub2api:

- `backend/internal/service/cn_provider_quota_service.go`: quota endpoints for
  zhipu / kimi / minimax coding plans and the response parsers (window
  classification by unit, kimi limits/usage, minimax model_remains). Used in
  `parse.go`, `probe.go` (QuotaURL) and `types.go`.
- `backend/internal/service/ratelimit_cn_providers.go`: wording and structured
  error type that identify a spent plan window (403/429), the
  concurrency-limit exclusion, and the balance-low text list. Used in
  `policy.go` (ClassifyError).

The Go code differs (own types, unix-second reset times, no shared state).
`testdata/*.json` are hand-written reconstructions of the response shapes with
invented values. LGPL-3.0 license text: https://www.gnu.org/licenses/lgpl-3.0.txt

## Account-pool operations (lane B2)

Design only; no source code was copied.

- tbphp/gpt-load (MIT): `internal/control/credential_import_batch.go` and
  `internal/control/credential_probe.go`. The staged dry-run / probe / confirm
  import with fingerprint dedupe, and the probe-then-restore proof.
  Reimplemented in `internal/adapter/handler/v2_channel_import.go` and
  `v2_channel_key_restore.go`.
- Wei-Shaw relay-service project (MIT; the repository name is omitted on purpose): session mapping with sliding TTL
  renewal. Reimplemented in `internal/app/key_affinity.go`.

## Relay data control (lane B4, migration 050, `internal/app/contentpolicy`)

Design only; no source code was copied.

- maximhq/bifrost (Apache-2.0): the layered content-logging switch (platform,
  tenant, token) where the strictest layer wins; also the regression class of
  a token-level decision overriding a stricter tenant one, guarded by tests.
  `internal/app/contentpolicy/retention.go`.
- Portkey-AI/gateway (MIT): the observe / enforce guardrail split.
  `internal/app/contentpolicy/rules.go`, `guard.go`.
- axonhub (Apache-2.0): the defect class where a pass-through path forwards
  the original body after the parsed copy was redacted, which is why masking is
  applied to the cached raw request body.
  `internal/adapter/handler/relay_content_rules.go`.

## Pool monitoring (lane B3) and per-account cost (lane B5, migration 051)

Design only; no source code was copied.

- BerriAI/litellm (mixed license, so nothing was taken beyond ideas): the
  metric and alert design for channel / account pools.
  `internal/pkg/metrics/channel_ops.go`, `doc/runbook/channel-pool-monitoring.md`.
- Per-account cost and utilization (`internal/app/channelusage`) is original
  work following `docs/plans/account-pool-ops-2026-10-09.md`.
