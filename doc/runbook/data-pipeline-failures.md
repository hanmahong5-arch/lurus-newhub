# Data-pipeline failure counters

Three counters make previously log-only (or Debug-only) losses visible. All
are on `/metrics` at 0 from boot.

| Series | Written by | Meaning |
|--------|-----------|---------|
| `lurus_log_search_sync_failed_total` | `internal/pkg/search/sync.go` (`SyncLogAsync`, `SyncLogsBatchAsync`) | A log document was not indexed in Meilisearch after the retry budget. The `logs` row is intact; only full-text search misses it. |
| `lurus_quota_data_write_failed_total` | `internal/adapter/repo/usedata.go` (`writeQuotaDataSnapshot`) | A `quota_data` aggregate bucket was dropped (not retried). Dashboards under-report usage; billing is unaffected. |
| `lurus_nats_publish_failed_total{subject_group}` | `internal/pkg/nats/publisher.go` (`Publish`) | An event publish failed. `subject_group` is `quota`, `usage`, `image` or `other`. A context timeout is counted too and can over-count against a slow-but-healthy broker. |

Alarms: `newhub_log_search_sync_failed`, `newhub_nats_publish_failed`
(`deploy/r6-host-netdata/health.d/newhub.conf`).

## Triage

- Search sync: check Meilisearch reachability and `MEILISEARCH_ENABLED`;
  rebuild the index once healthy. No money impact.
- quota_data: look for `saveQuotaData create error` / `increaseQuotaData error`
  in the replica log for the database error; the dropped buckets are not
  recoverable from the cache, rebuild from `logs` if the dashboard matters.
- NATS: check broker health and the group that is failing; consumers key on
  event identity, so a later redelivery is idempotent.
