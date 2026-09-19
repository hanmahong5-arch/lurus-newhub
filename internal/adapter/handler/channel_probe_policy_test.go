package handler

// channel_probe_policy_test.go — L3 oracle for evaluateProbeOutcome
// (channel_probe_policy.go), driven directly with hand-built testResult
// values so the hysteresis/classification policy is provable without a
// real upstream or a database. probeChannel's own HTTP mechanics —
// including the deadline that produces a real context.DeadlineExceeded —
// are covered separately by channel_probe_auto_test.go's
// TestProbeChannel_DeadlineBoundsHangingUpstream.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// withAutomaticDisableChannelEnabled sets common.AutomaticDisableChannelEnabled
// for the duration of the test and restores it after.
func withAutomaticDisableChannelEnabled(t *testing.T, v bool) {
	t.Helper()
	prev := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = v
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prev })
}

// stubSoleEnabledModels overrides the soleEnabledModelsFn seam for the
// duration of the test.
func stubSoleEnabledModels(t *testing.T, fn func(channelID int, tenantID string) ([]repo.SoleModel, error)) {
	t.Helper()
	prev := soleEnabledModelsFn
	soleEnabledModelsFn = fn
	t.Cleanup(func() { soleEnabledModelsFn = prev })
}

func TestEvaluateProbeOutcome_LatencyBanNeedsThreeConsecutiveBreaches(t *testing.T) {
	withAutomaticDisableChannelEnabled(t, true)
	stubSoleEnabledModels(t, func(int, string) ([]repo.SoleModel, error) { return nil, nil })

	channel := &repo.Channel{Id: 4101, Type: 1, TenantId: "default"}
	tracker := newLatencyBreachTracker()
	slow := testResult{} // no error — a technically clean HTTP response, just slow
	const threshold = 10 * time.Millisecond
	const elapsed = 50 * time.Millisecond

	v1 := evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker)
	if v1.BanReason != "" {
		t.Fatalf("breach 1: BanReason = %q, want \"\" (needs 3 consecutive)", v1.BanReason)
	}
	if v1.Streak != 1 {
		t.Fatalf("breach 1: Streak = %d, want 1", v1.Streak)
	}

	v2 := evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker)
	if v2.BanReason != "" {
		t.Fatalf("breach 2: BanReason = %q, want \"\" (needs 3 consecutive)", v2.BanReason)
	}
	if v2.Streak != 2 {
		t.Fatalf("breach 2: Streak = %d, want 2", v2.Streak)
	}

	v3 := evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker)
	if v3.BanReason != "latency" {
		t.Fatalf("breach 3: BanReason = %q, want \"latency\"", v3.BanReason)
	}
	if v3.Streak != 3 {
		t.Fatalf("breach 3: Streak = %d, want 3", v3.Streak)
	}

	// A clean pass in between resets the streak — two breaches after a
	// clean pass must NOT ban (proves Reset is wired, not just that the
	// threshold constant is 3).
	clean := testResult{}
	const fast = 1 * time.Millisecond
	tracker2 := newLatencyBreachTracker()
	evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker2)
	evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker2)
	resetVerdict := evaluateProbeOutcome(channel, clean, fast, threshold, tracker2)
	if resetVerdict.Outcome != probeOutcomeOK {
		t.Fatalf("clean pass: Outcome = %q, want ok", resetVerdict.Outcome)
	}
	afterReset := evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker2)
	if afterReset.Streak != 1 {
		t.Fatalf("breach after reset: Streak = %d, want 1 (clean pass must have zeroed the streak)", afterReset.Streak)
	}
	if afterReset.BanReason != "" {
		t.Fatalf("breach after reset: BanReason = %q, want \"\" (only 1 breach since reset)", afterReset.BanReason)
	}
}

func TestEvaluateProbeOutcome_SoleChannelNeverLatencyBanned(t *testing.T) {
	withAutomaticDisableChannelEnabled(t, true)
	stubSoleEnabledModels(t, func(channelID int, tenantID string) ([]repo.SoleModel, error) {
		return []repo.SoleModel{{Group: "default", Model: "rt-alpha"}}, nil
	})

	channel := &repo.Channel{Id: 4102, Type: 1, TenantId: "default"}
	tracker := newLatencyBreachTracker()
	slow := testResult{}
	const threshold = 10 * time.Millisecond
	const elapsed = 50 * time.Millisecond

	var last probeVerdict
	for i := 0; i < latencyBreachStreakThreshold; i++ {
		last = evaluateProbeOutcome(channel, slow, elapsed, threshold, tracker)
	}

	if last.Streak != latencyBreachStreakThreshold {
		t.Fatalf("Streak = %d, want %d", last.Streak, latencyBreachStreakThreshold)
	}
	if last.BanReason != "" {
		t.Fatalf("BanReason = %q, want \"\" (sole-enabled channel must never be latency-banned)", last.BanReason)
	}
	if len(last.SoleModels) != 1 || last.SoleModels[0].Model != "rt-alpha" {
		t.Fatalf("SoleModels = %+v, want [{default rt-alpha}]", last.SoleModels)
	}
}

func TestEvaluateProbeOutcome_TimeoutIsLatencyNotError(t *testing.T) {
	withAutomaticDisableChannelEnabled(t, true)
	stubSoleEnabledModels(t, func(int, string) ([]repo.SoleModel, error) { return nil, nil })

	channel := &repo.Channel{Id: 4103, Type: 1, TenantId: "default"}
	tracker := newLatencyBreachTracker()

	// Mirrors probeChannel's real wrapping chain: adaptor.DoRequest wraps
	// the context error, and the caller wraps that into a NewAPIError —
	// so newAPIError IS non-nil for a real timeout, exactly like a real
	// error-class failure. If evaluateProbeOutcome did not special-case
	// isTimeout, this would risk being routed through
	// app.ShouldDisableChannel and banned immediately as an "error",
	// bypassing the 3-breach hysteresis entirely.
	wrapped := fmt.Errorf("do request failed: %w", context.DeadlineExceeded)
	timeoutResult := testResult{
		localErr:    wrapped,
		newAPIError: types.NewOpenAIError(wrapped, types.ErrorCodeDoRequestFailed, 500),
	}
	const threshold = 10 * time.Millisecond
	const elapsed = 200 * time.Millisecond // channelProbeTimeout firing, well over any latency threshold

	v1 := evaluateProbeOutcome(channel, timeoutResult, elapsed, threshold, tracker)
	if v1.Outcome != probeOutcomeTimeout {
		t.Fatalf("Outcome = %q, want %q", v1.Outcome, probeOutcomeTimeout)
	}
	if v1.BanReason == "error" {
		t.Fatalf("BanReason = %q, want anything but \"error\" (a timeout must take the latency path, not the immediate error-ban path)", v1.BanReason)
	}
	if v1.Streak != 1 {
		t.Fatalf("breach 1: Streak = %d, want 1", v1.Streak)
	}

	v2 := evaluateProbeOutcome(channel, timeoutResult, elapsed, threshold, tracker)
	v3 := evaluateProbeOutcome(channel, timeoutResult, elapsed, threshold, tracker)
	if v2.BanReason != "" {
		t.Fatalf("breach 2: BanReason = %q, want \"\"", v2.BanReason)
	}
	if v3.BanReason != "latency" {
		t.Fatalf("breach 3: BanReason = %q, want \"latency\" (3 consecutive timeouts is a latency ban, never an error ban)", v3.BanReason)
	}
}

// TestEvaluateProbeOutcome_ErrorClassBansOnTheFirstPass is the L3
// repair-round oracle the acceptor's audit found missing (verdict finding
// #1): deleting the error-class ban branch in evaluateProbeOutcome (the
// `result.newAPIError != nil && common.AutomaticDisableChannelEnabled &&
// app.ShouldDisableChannel(...)` check) must turn this red. An
// invalid_api_key result bans on the FIRST pass — no streak required,
// unlike the latency path — and resets any latency streak already in
// progress, since an error-class ban is unrelated to the latency pattern
// the streak tracks.
func TestEvaluateProbeOutcome_ErrorClassBansOnTheFirstPass(t *testing.T) {
	withAutomaticDisableChannelEnabled(t, true)
	stubSoleEnabledModels(t, func(int, string) ([]repo.SoleModel, error) {
		t.Fatal("soleEnabledModelsFn must not be consulted for an error-class ban — the sole-channel guard only protects the latency path")
		return nil, nil
	})

	channel := &repo.Channel{Id: 4104, Type: 1, TenantId: "default"}
	tracker := newLatencyBreachTracker()
	// Seed an in-progress latency streak first, so a later assertion can
	// prove the error-class branch resets it rather than just never having
	// touched it.
	tracker.Breach(channel.Id)
	tracker.Breach(channel.Id)

	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message: "invalid api key provided",
		Type:    "invalid_request_error",
		Code:    "invalid_api_key",
	}, http.StatusUnauthorized)
	result := testResult{
		localErr:    errors.New("do request failed: upstream error: do request failed"),
		newAPIError: apiErr,
	}

	v := evaluateProbeOutcome(channel, result, 5*time.Millisecond, 10*time.Millisecond, tracker)
	if v.BanReason != "error" {
		t.Fatalf("BanReason = %q, want %q (invalid_api_key must ban on the first occurrence)", v.BanReason, "error")
	}
	if v.Streak != 0 {
		t.Fatalf("Streak = %d, want 0 (an error-class ban is not a latency streak, the field is never set on this path)", v.Streak)
	}
	if v.Outcome != probeOutcomeError {
		t.Fatalf("Outcome = %q, want %q", v.Outcome, probeOutcomeError)
	}
	if v.Err != apiErr {
		t.Fatalf("Err = %v, want the original newAPIError", v.Err)
	}

	// Tracker reset: a subsequent slow-but-error-free breach starts back at
	// 1, proving the error-class branch's tracker.Reset actually ran rather
	// than the pre-seeded streak of 2 still being live.
	slow := testResult{}
	after := evaluateProbeOutcome(channel, slow, 50*time.Millisecond, 10*time.Millisecond, tracker)
	if after.Streak != 1 {
		t.Fatalf("breach after error-class ban: Streak = %d, want 1 (the error-class branch must have reset the pre-seeded streak of 2)", after.Streak)
	}
}

// TestEvaluateProbeOutcome_TimeoutDoesNotBanWhenAutoDisableIsOff is the L3
// repair-round oracle for verdict finding #5 (minor): before this fix, a
// TIMEOUT always entered the latency path regardless of
// common.AutomaticDisableChannelEnabled (only the "technically successful
// but slow" overThreshold branch checked the flag), so three consecutive
// timeouts still produced BanReason="latency" with the setting off. The
// streak itself still increments either way (bookkeeping, not a ban
// decision) — only BanReason, and the soleEnabledModelsFn consultation that
// precedes it, must stay gated off.
func TestEvaluateProbeOutcome_TimeoutDoesNotBanWhenAutoDisableIsOff(t *testing.T) {
	withAutomaticDisableChannelEnabled(t, false)
	stubSoleEnabledModels(t, func(int, string) ([]repo.SoleModel, error) {
		t.Fatal("soleEnabledModelsFn must not be consulted when AutomaticDisableChannelEnabled is off — there is no ban decision to protect")
		return nil, nil
	})

	channel := &repo.Channel{Id: 4105, Type: 1, TenantId: "default"}
	tracker := newLatencyBreachTracker()

	wrapped := fmt.Errorf("do request failed: %w", context.DeadlineExceeded)
	timeoutResult := testResult{
		localErr:    wrapped,
		newAPIError: types.NewOpenAIError(wrapped, types.ErrorCodeDoRequestFailed, 500),
	}
	const threshold = 10 * time.Millisecond
	const elapsed = 200 * time.Millisecond

	var last probeVerdict
	for i := 0; i < latencyBreachStreakThreshold; i++ {
		last = evaluateProbeOutcome(channel, timeoutResult, elapsed, threshold, tracker)
	}

	if last.Streak != latencyBreachStreakThreshold {
		t.Fatalf("Streak = %d, want %d (streak bookkeeping runs independent of the ban gate)", last.Streak, latencyBreachStreakThreshold)
	}
	if last.BanReason != "" {
		t.Fatalf("BanReason = %q, want \"\" (AutomaticDisableChannelEnabled is off — the ban decision must be gated exactly like the error-class branch)", last.BanReason)
	}
	if last.Outcome != probeOutcomeTimeout {
		t.Fatalf("Outcome = %q, want %q (still classified as a timeout for observability even though no ban follows)", last.Outcome, probeOutcomeTimeout)
	}
}

// TestChannelProbeTimeout_ClampsNonPositiveToDefault is the L3 repair-round
// oracle for verdict finding #6 (minor): common.GetEnvOrDefault only falls
// back to the default on an UNPARSABLE value, so CHANNEL_TEST_TIMEOUT_SECONDS
// set to "0" or "-1" parsed successfully and produced a zero/negative
// deadline — every probe timed out before the first byte. "garbage" already
// worked correctly before this fix (GetEnvOrDefault's own unparsable-value
// fallback), included here so the table proves all three inputs converge on
// the same 120s default via channelProbeTimeout specifically.
func TestChannelProbeTimeout_ClampsNonPositiveToDefault(t *testing.T) {
	cases := []string{"0", "-1", "not-a-number"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			prev, had := os.LookupEnv(channelProbeTimeoutEnv)
			if err := os.Setenv(channelProbeTimeoutEnv, v); err != nil {
				t.Fatalf("setenv: %v", err)
			}
			t.Cleanup(func() {
				if had {
					_ = os.Setenv(channelProbeTimeoutEnv, prev)
				} else {
					_ = os.Unsetenv(channelProbeTimeoutEnv)
				}
			})
			if got := channelProbeTimeout(); got != 120*time.Second {
				t.Fatalf("channelProbeTimeout() with %s=%q = %s, want 120s", channelProbeTimeoutEnv, v, got)
			}
		})
	}
}

func TestClassifyProbeResult(t *testing.T) {
	cases := []struct {
		name string
		in   testResult
		want probeOutcome
	}{
		{"clean", testResult{}, probeOutcomeOK},
		{"error", testResult{localErr: fmt.Errorf("boom"), newAPIError: types.NewOpenAIError(fmt.Errorf("boom"), types.ErrorCodeDoRequestFailed, 500)}, probeOutcomeError},
		{"timeout", testResult{localErr: fmt.Errorf("do request failed: %w", context.DeadlineExceeded)}, probeOutcomeTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyProbeResult(tc.in); got != tc.want {
				t.Errorf("classifyProbeResult(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
