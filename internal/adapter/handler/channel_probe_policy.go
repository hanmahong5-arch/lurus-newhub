package handler

// channel_probe_policy.go — L3 (cycle 11): the decision layer the automatic
// probe pass turns one probe's result into. Separated from probeChannel's
// HTTP mechanics (channel-test.go) so hysteresis, sole-channel protection,
// and error-vs-latency classification can be driven directly in tests
// without a real upstream — see channel_probe_policy_test.go.
//
// Chain: AutomaticallyTestChannelsWithContext (leader-gated ticker) and
// TestAllChannels (operator "test all channels now" button) both call
// testAllChannels, whose gopool.Go loop calls autoProbeChannel once per
// channel; autoProbeChannel calls probeChannel then evaluateProbeOutcome,
// and turns the verdict into metrics + the same processChannelError /
// app.EnableChannel calls the pre-cycle-11 code made inline.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// channelProbeTimeoutEnv bounds a single probe's upstream call.
// channelProbeTimeout reads it fresh on every probe (not cached), the same
// convention operation_setting values follow elsewhere in this file, so an
// operator's env change takes effect without a restart.
const channelProbeTimeoutEnv = "CHANNEL_TEST_TIMEOUT_SECONDS"

// channelProbeTimeout returns CHANNEL_TEST_TIMEOUT_SECONDS, defaulting to
// 120s (and clamping any parsed value <= 0, e.g. an operator setting the
// env var to "0", to the same default — GetEnvOrDefault only falls back on
// an UNPARSABLE value, so "0" or "-1" would otherwise pass through as a
// zero/negative deadline that expires before the first byte). Before this
// bound existed, probeChannel's predecessor (testChannel) built its
// *http.Request with no context at all, so every downstream
// c.Request.Context() call resolved to context.Background() — genuinely
// unbounded. The relay transport itself already bounds two narrower cases —
// RelayResponseHeaderTimeout (90s to the first response header,
// internal/pkg/common/init.go) and the 300s trickling-body read timeout
// (internal/app/http_client.go) — but nothing bounded the combination
// end-to-end before this (the 2026-09-17 audit's field note records one
// probe against a hung upstream running about 900s). This deadline adds a
// single budget for the whole probe, including
// the body read, configurable per deployment without a restart (read fresh
// on every call).
func channelProbeTimeout() time.Duration {
	seconds := common.GetEnvOrDefault(channelProbeTimeoutEnv, 120)
	if seconds <= 0 {
		common.SysError(fmt.Sprintf("%s=%d is not a positive number of seconds, using default 120", channelProbeTimeoutEnv, seconds))
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

// probeOutcome labels metrics.RecordChannelProbe. "timeout" and
// "latency_breach" are deliberately distinct: a timeout is
// channelProbeTimeout() firing before any response arrived; a latency
// breach is a technically successful response that took longer than the
// automatic pass's disable threshold — a comparison only the automatic
// pass makes (probeChannel itself has no threshold to compare against, so
// classifyProbeResult below never returns it).
type probeOutcome string

const (
	probeOutcomeOK            probeOutcome = "ok"
	probeOutcomeError         probeOutcome = "error"
	probeOutcomeTimeout       probeOutcome = "timeout"
	probeOutcomeLatencyBreach probeOutcome = "latency_breach"
)

// classifyProbeResult buckets a probeChannel result into ok/error/timeout —
// no notion of a latency threshold, since only evaluateProbeOutcome (the
// automatic pass) has one to compare against. Shared by the manual
// TestChannel handler (which records metrics straight off this) and
// evaluateProbeOutcome (which further refines "ok" into "latency_breach").
func classifyProbeResult(result testResult) probeOutcome {
	if probeResultIsTimeout(result) {
		return probeOutcomeTimeout
	}
	if result.localErr != nil || result.newAPIError != nil {
		return probeOutcomeError
	}
	return probeOutcomeOK
}

// probeResultIsTimeout reports whether the probe's own deadline
// (channelProbeTimeout) fired, rather than the upstream returning an
// ordinary error. Two checks, because the provider layer deliberately
// sanitizes network errors before they leave probeChannel:
// internal/adapter/provider/api_request.go's doRequest wraps every
// client.Do failure with ErrOptionWithHideErrMsg("upstream error: do
// request failed") — which REPLACES the error's Err field (and therefore
// its errors.Is/Unwrap chain) with a fresh, generic error, by design, so a
// probe failure never leaks upstream connection internals. That means
// errors.Is(result.localErr, context.DeadlineExceeded) is NOT reliable for
// the common case; result.context carries the *http.Request this probe
// actually sent (c.Request = c.Request.WithContext(...) in probeChannel),
// and its Context().Err() reports the deadline directly, independent of
// how any layer above it chose to word the error. The errors.Is fallback
// stays for any call path that returns a context error un-sanitized.
func probeResultIsTimeout(result testResult) bool {
	if result.context != nil && result.context.Request != nil && errors.Is(result.context.Request.Context().Err(), context.DeadlineExceeded) {
		return true
	}
	return result.localErr != nil && errors.Is(result.localErr, context.DeadlineExceeded)
}

// probeVerdict is evaluateProbeOutcome's decision for one automatic probe
// pass over one channel.
type probeVerdict struct {
	// Err is non-nil for every latency breach (timeout or over-threshold)
	// and for an error-class result THAT ALREADY CARRIED a *types.NewAPIError
	// — it is NOT set merely because classifyProbeResult returned "error":
	// a result that failed before probeChannel ever produced a NewAPIError
	// (e.g. the unsupported-channel-type return, or the user-cache lookup
	// failure — both set only localErr) leaves Err nil here. That gap is why
	// autoProbeChannel's re-enable check does not rely on Err alone — it
	// also requires the underlying testResult.localErr == nil, mirroring
	// upstream New API's own guard, so a probe that never reached the
	// upstream can never be read as "clean" and re-enable a disabled
	// channel. Within the latency path Err is always non-nil, so it alone
	// is sufficient there: a single slow pass already stops a stale-disabled
	// channel from being silently re-enabled by the very probe that just
	// ran slow again.
	Err *types.NewAPIError
	// BanReason is "" (no ban this pass), "error" (app.ShouldDisableChannel
	// matched — banned immediately, no streak needed, unchanged from
	// before this cycle), or "latency" (the streak reached
	// latencyBreachStreakThreshold and the channel is not the sole enabled
	// channel for any (group, model) pair it serves).
	BanReason string
	// Streak is the consecutive latency-breach count after this pass (0
	// unless this pass entered the latency path).
	Streak int
	// SoleModels is non-empty when a latency ban was withheld because
	// soleEnabledModelsFn found at least one (group, model) pair only this
	// channel can serve.
	SoleModels []repo.SoleModel
	// Outcome labels metrics.RecordChannelProbe.
	Outcome probeOutcome
}

// latencyBreachStreakThreshold: one slow probe is noise; three consecutive
// ones against the same channel, with no clean pass or error-class ban in
// between, are a pattern (cycle-11 plan operator_decisions).
const latencyBreachStreakThreshold = 3

// latencyBreachTracker counts consecutive over-threshold passes per
// channel ID. Process-local. Two callers feed the SAME package-level
// instance (autoLatencyBreachTracker): the leader-gated ticker
// (AutomaticallyTestChannelsWithContext's common.IsLeader() gate) AND the
// operator-triggered "test all channels" button (TestAllChannels ->
// testAllChannels -> autoProbeChannel), which answers on WHICHEVER replica
// served that HTTP request, not only the leader. Because the tracker is
// process-local, streaks are per-replica: three "test all channels" clicks
// that happen to land on the same follower complete a ban the leader never
// saw, and a leadership change hands the ticker to a different process with
// an empty tracker — streaks are NOT persisted across either event
// (documented, not treated as a defect — see doc/runbook/channel-auto-ban.md).
type latencyBreachTracker struct {
	mu      sync.Mutex
	streaks map[int]int
}

func newLatencyBreachTracker() *latencyBreachTracker {
	return &latencyBreachTracker{streaks: make(map[int]int)}
}

// Breach records one more consecutive breach for channelID and returns the
// new streak length.
func (t *latencyBreachTracker) Breach(channelID int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.streaks[channelID]++
	return t.streaks[channelID]
}

// Reset clears channelID's streak. Called on any clean pass or error-class
// ban — both unrelated to the latency pattern the streak is tracking.
func (t *latencyBreachTracker) Reset(channelID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streaks, channelID)
}

// autoLatencyBreachTracker is the tracker the production automatic pass
// shares across ticks. Tests construct their own via newLatencyBreachTracker
// so one test's streaks cannot leak into another's.
var autoLatencyBreachTracker = newLatencyBreachTracker()

// soleEnabledModelsFn is a seam over repo.SoleEnabledModelsForChannel so
// evaluateProbeOutcome's own tests can drive it without a database.
// Production leaves it as the real repo call.
var soleEnabledModelsFn = repo.SoleEnabledModelsForChannel

// evaluateProbeOutcome turns one probe's result into a probeVerdict. Its
// only side effects are on tracker (an explicit, injected seam) and
// through the soleEnabledModelsFn seam — everything else is a function of
// its arguments.
func evaluateProbeOutcome(channel *repo.Channel, result testResult, elapsed time.Duration, threshold time.Duration, tracker *latencyBreachTracker) probeVerdict {
	base := classifyProbeResult(result)

	if base == probeOutcomeError {
		// Error-class: app.ShouldDisableChannel's contract is unchanged.
		// Bans immediately — no streak needed, matching pre-cycle-11
		// behaviour for this class of failure.
		if result.newAPIError != nil && common.AutomaticDisableChannelEnabled && app.ShouldDisableChannel(channel.Type, result.newAPIError) {
			tracker.Reset(channel.Id)
			return probeVerdict{Err: result.newAPIError, BanReason: "error", Outcome: probeOutcomeError}
		}
		return probeVerdict{Err: result.newAPIError, Outcome: probeOutcomeError}
	}

	// Latency path: a timeout always enters it (operator_decisions:
	// "TimeoutIsLatencyNotError" — even though the wrapped error's message
	// could accidentally match an AutomaticDisableKeywords entry, a
	// timeout is never routed through app.ShouldDisableChannel above,
	// because classifyProbeResult returns "timeout" not "error" for it).
	// A clean HTTP response enters it only when it ran over threshold with
	// auto-disable on — mirrors the pre-cycle-11
	// "AutomaticDisableChannelEnabled && !shouldBanChannel" gate. Entry into
	// this branch (and the streak counter within it) is intentionally NOT
	// gated on AutomaticDisableChannelEnabled for the timeout case — a
	// timeout is always latency-classified for the outcome/streak
	// bookkeeping regardless of the flag, so the observable metrics stay
	// meaningful even with auto-disable off. Only the BAN DECISION below is
	// gated, exactly the way the error-class branch above gates on the same
	// flag (there, indirectly, via app.ShouldDisableChannel's own internal
	// check).
	overThreshold := common.AutomaticDisableChannelEnabled && threshold > 0 && elapsed > threshold
	if base == probeOutcomeTimeout || overThreshold {
		streak := tracker.Breach(channel.Id)

		outcome := probeOutcomeLatencyBreach
		errMsg := fmt.Errorf("响应时间 %s 超过阈值 %s", elapsed, threshold)
		if base == probeOutcomeTimeout {
			outcome = probeOutcomeTimeout
			errMsg = fmt.Errorf("探活超时（超过 %s）: %w", channelProbeTimeout(), result.localErr)
		}

		verdict := probeVerdict{
			Err:     types.NewOpenAIError(errMsg, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout),
			Streak:  streak,
			Outcome: outcome,
		}

		// Ban-decision gate (cycle-11 L3 repair round): without this, three
		// consecutive TIMEOUTS still produced BanReason="latency" even with
		// AutomaticDisableChannelEnabled off, because only overThreshold
		// above (the slow-but-successful-response case) checked the flag —
		// entry into this branch via a raw timeout did not. The DB status
		// flip was still blocked downstream (processChannelError re-checks
		// app.ShouldDisableChannel, which itself returns false when the flag
		// is off), so this was never an availability regression — but
		// autoProbeChannel's disable_latency counter fired first, over
		// counting bans that never happened. Streak tracking above still
		// runs either way, so turning the flag on mid-outage does not lose
		// count of an in-progress pattern.
		if streak >= latencyBreachStreakThreshold && common.AutomaticDisableChannelEnabled {
			sole, err := soleEnabledModelsFn(channel.Id, channel.TenantId)
			if err != nil {
				// Fail closed on the PROTECTION, not the ban: unable to
				// determine sole-channel status, so do not risk banning
				// what might be the only route for a model. Streak is
				// already recorded above; the next clean pass (or the
				// next successful sole-channel lookup) resolves it.
				common.SysLog(fmt.Sprintf("channel probe: SoleEnabledModelsForChannel(%d) failed, withholding latency ban this pass: %v", channel.Id, err))
				return verdict
			}
			if len(sole) > 0 {
				verdict.SoleModels = sole
				return verdict
			}
			verdict.BanReason = "latency"
		}
		return verdict
	}

	// Clean pass.
	tracker.Reset(channel.Id)
	return probeVerdict{Outcome: probeOutcomeOK}
}

// autoProbeChannel is the automatic pass's per-channel body — the gopool.Go
// loop in testAllChannels used to inline all of this directly. Always
// probes with RecordConsumeLog: false (cycle-11 plan §L3): the only
// probeChannel caller that still writes a consume-log row is the manual
// GET /api/channel/test/:id path (testChannel).
func autoProbeChannel(channel *repo.Channel, threshold time.Duration) {
	isChannelEnabled := channel.Status == common.ChannelStatusEnabled

	tik := time.Now()
	result := probeChannel(channel, "", "", channelProbeOptions{RecordConsumeLog: false})
	elapsed := time.Since(tik)

	verdict := evaluateProbeOutcome(channel, result, elapsed, threshold, autoLatencyBreachTracker)
	metrics.RecordChannelProbe(string(verdict.Outcome), elapsed)

	if len(verdict.SoleModels) > 0 {
		metrics.RecordChannelAutoStatus("latency_ban_skipped_sole_channel")
	}

	if isChannelEnabled && verdict.BanReason != "" && channel.GetAutoBan() {
		action := "disable_error"
		if verdict.BanReason == "latency" {
			action = "disable_latency"
		}
		metrics.RecordChannelAutoStatus(action)
		processChannelError(
			result.context,
			*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()),
			verdict.Err,
		)
	}

	// result.localErr == nil (cycle-11 L3 repair round, mirrors upstream New
	// API's own "result.localErr == nil && ..." guard before its enable
	// call): verdict.Err alone is NOT sufficient here — probeVerdict.Err's
	// own doc explains why it can be nil even for a genuine failure (a
	// result that never reached probeChannel's upstream call, e.g. the
	// unsupported-channel-type return or a user-cache lookup failure, sets
	// only localErr). Without this, such a probe reads as indistinguishable
	// from a real clean pass and could silently re-enable an AutoDisabled
	// channel that was never actually tested.
	if !isChannelEnabled && result.localErr == nil && app.ShouldEnableChannel(verdict.Err, channel.Status) {
		metrics.RecordChannelAutoStatus("enable")
		app.EnableChannel(channel.Id, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.Name)
	}

	channel.UpdateResponseTime(elapsed.Milliseconds())
}
