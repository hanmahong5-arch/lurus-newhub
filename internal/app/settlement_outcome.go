package app

// settlement_outcome.go — a failed settlement no longer masquerades as a
// clean charge, on the three consume-quota sites this cycle routes through
// SettleConsume: this package's PostClaudeConsumeQuota/PostAudioConsumeQuota
// and relay.postConsumeQuota. Before this file each of those three inlined
// the same three lines: call PostConsumeQuota, log the error if any, move
// on — and then write a full consume log row regardless. Other
// PostConsumeQuota callers (internal/app/relay/mjproxy_handler.go:223,:526,
// internal/app/relay/relay_task.go:208) and the realtime path
// (quota.go:231 PostWssConsumeQuota, which does not call PostConsumeQuota
// as of 2026-09-19) are not touched by this file and are not counted below — see
// doc/runbook/settlement-failed.md's "Not covered this cycle" section. That
// money-moving call itself is unchanged here; what changed is that nothing
// on the row or on /metrics used to tell a reader the settlement behind the
// row's quota number might not have landed.

import (
	"context"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// PostConsumeQuotaFn is a test seam over PostConsumeQuota, same convention as
// AsyncGo (quota.go:42) — SettleConsume below is exercisable without driving
// PostConsumeQuota's own DB/wallet side effects.
var PostConsumeQuotaFn = PostConsumeQuota

// SettleConsume is the settlement call for the three consume-quota sites
// that route through it this cycle: relay's OpenAI-compatible text path,
// PostClaudeConsumeQuota, PostAudioConsumeQuota. Other PostConsumeQuota
// callers are not counted here — see this file's header comment. On error
// it keeps the exact log line each site wrote inline before this change,
// and additionally increments metrics.BillingSettlementFailedTotal for
// path — the counter this cycle adds so a settlement failure is visible on
// /metrics, not only in a log line nobody is paged on. It does not change
// what PostConsumeQuotaFn does or what it debits.
func SettleConsume(ctx context.Context, relayInfo *relaycommon.RelayInfo, quotaDelta, preConsumedQuota int, path string) error {
	err := PostConsumeQuotaFn(relayInfo, quotaDelta, preConsumedQuota, true)
	if err != nil {
		logger.LogError(ctx, "error consuming token remain quota: "+err.Error())
		metrics.BillingSettlementFailedTotal.WithLabelValues(path).Inc()
	}
	return err
}

// FlagSettlementOutcome marks a log row's Other payload when the settlement
// call for that row failed. The row is still written at the same quota it
// would have carried anyway (RecordConsumeLog and the debit path are both
// untouched) — this is the only signal on the row itself that the charge it
// shows may not have actually settled; the logger.LogError line SettleConsume
// also writes is a second, separate signal (see this file's header comment).
// Leaves other untouched on success, so the key is only ever present when
// there is something to say about it.
func FlagSettlementOutcome(other map[string]interface{}, settleErr error) {
	if settleErr != nil {
		other["settlement"] = "failed"
	}
}
