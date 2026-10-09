package planquota

import (
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// DefaultThresholdPct is the used-percent at which a window parks a channel
// when neither the channel nor the environment overrides it.
const DefaultThresholdPct = 95.0

// ThresholdFor returns the effective threshold of a channel: its own
// plan_threshold_pct when in (0,100], else the global default.
func ThresholdFor(s dto.ChannelSettings, global float64) float64 {
	if s.PlanThresholdPct > 0 && s.PlanThresholdPct <= 100 {
		return s.PlanThresholdPct
	}
	if global > 0 && global <= 100 {
		return global
	}
	return DefaultThresholdPct
}

// ConservativeReset is the cooldown end used when no window reset is known:
// the next whole hour plus five minutes.
func ConservativeReset(now time.Time) int64 {
	return now.Truncate(time.Hour).Add(time.Hour + 5*time.Minute).Unix()
}

// EvalThreshold decides whether the snapshot's windows require parking the
// channel. A window counts when used_pct >= threshold and it has not reset yet
// (reset_at in the future, or unknown). With several hit windows the latest
// reset wins, because the channel is unusable until all of them reset. A hit
// window with an unknown reset uses ConservativeReset.
func EvalThreshold(windows []Window, threshold float64, now time.Time) (until int64, hit bool) {
	n := now.Unix()
	for _, w := range windows {
		if w.UsedPct < threshold {
			continue
		}
		reset := w.ResetAt
		if reset != 0 && reset <= n {
			continue // already reset; the snapshot is stale for this window
		}
		if reset == 0 {
			reset = ConservativeReset(now)
		}
		hit = true
		if reset > until {
			until = reset
		}
	}
	return until, hit
}

// CooldownTarget picks the cooldown end after an upstream "window used up"
// answer. Windows at 100% or more decide first (latest reset among them); if
// none is full, the earliest future reset is used (the rejection most likely
// came from the shortest window); with no usable reset the conservative value.
func CooldownTarget(windows []Window, now time.Time) int64 {
	n := now.Unix()
	var full int64
	var earliest int64
	for _, w := range windows {
		if w.ResetAt <= n {
			continue
		}
		if w.UsedPct >= 100 && w.ResetAt > full {
			full = w.ResetAt
		}
		if earliest == 0 || w.ResetAt < earliest {
			earliest = w.ResetAt
		}
	}
	switch {
	case full > 0:
		return full
	case earliest > 0:
		return earliest
	}
	return ConservativeReset(now)
}

// ErrorClass is the verdict on an upstream failure of a plan channel.
type ErrorClass int

const (
	ClassNone ErrorClass = iota
	// ClassWindowExhausted: the plan's rolling window is used up; recovers at
	// the window reset. Never a reason to disable the channel.
	ClassWindowExhausted
	// ClassBalanceLow: out of balance; recoverable after a top-up.
	ClassBalanceLow
)

// kimiConcurrencyMessage is the Kimi 403 for too many parallel requests. It
// is a different condition from a spent window and is left to the generic path.
const kimiConcurrencyMessage = "You've reached your concurrent request limit."

// quotaExhaustedErrorType is the structured error type Kimi uses for a spent
// window.
const quotaExhaustedErrorType = "access_terminated_error"

// ClassifyError inspects an upstream failure (HTTP status, structured error
// type, message/body text) of a channel that declared a plan. Wording adapted
// from sub2api ratelimit_cn_providers.go (LGPL-3.0).
func ClassifyError(status int, errType, text string) ErrorClass {
	if status != 402 && status != 403 && status != 429 {
		return ClassNone
	}
	low := strings.ToLower(text)
	if strings.Contains(low, strings.ToLower(kimiConcurrencyMessage)) {
		return ClassNone
	}
	if isBalanceText(low) {
		return ClassBalanceLow
	}
	if status == 402 {
		return ClassNone
	}
	if strings.Contains(low, "usage limit") || strings.Contains(low, "quota will reset") ||
		strings.EqualFold(strings.TrimSpace(errType), quotaExhaustedErrorType) {
		return ClassWindowExhausted
	}
	return ClassNone
}

func isBalanceText(low string) bool {
	return strings.Contains(low, "余额不足") ||
		strings.Contains(low, "insufficient balance") ||
		strings.Contains(low, "insufficient_credit") ||
		strings.Contains(low, "balance is not enough") ||
		strings.Contains(low, "no enough balance")
}

// BalanceLowCooldown is how long a balance-low answer parks a channel; the
// next failing request re-arms it, a top-up needs no operator action.
const BalanceLowCooldown = 20 * time.Minute

// ---- expiry ----

// ExpiryAction is what the periodic scan should do for one channel.
type ExpiryAction int

const (
	ExpiryNone ExpiryAction = iota
	ExpiryPause
	ExpiryNotice72
	ExpiryNotice24
	// ExpiryRenew: the channel was paused by expiry but expires_at moved into
	// the future again.
	ExpiryRenew
)

// ExpiryReasonPrefix marks a status_reason written by the expiry scan, so it
// can be told from a manual or error-driven disable.
const ExpiryReasonPrefix = "expired:"

// DecideExpiry is the pure decision of the expiry scan. lastNotice is the
// highest notice level (72 or 24, 0 = none) already recorded for this very
// expires_at; pausedByExpiry says the channel currently sits disabled with an
// expired: reason.
func DecideExpiry(expiresAt int64, now time.Time, lastNotice int, pausedByExpiry bool) ExpiryAction {
	if expiresAt <= 0 {
		if pausedByExpiry {
			return ExpiryRenew
		}
		return ExpiryNone
	}
	n := now.Unix()
	if expiresAt <= n {
		if pausedByExpiry {
			return ExpiryNone
		}
		return ExpiryPause
	}
	if pausedByExpiry {
		return ExpiryRenew
	}
	left := time.Duration(expiresAt-n) * time.Second
	switch {
	case left <= 24*time.Hour && lastNotice != 24:
		return ExpiryNotice24
	case left > 24*time.Hour && left <= 72*time.Hour && lastNotice == 0:
		return ExpiryNotice72
	}
	return ExpiryNone
}

// ---- scheduled test mode ----

// Test modes for scheduled channel tests.
const (
	TestModeAll         = "all"
	TestModeAutoBanOnly = "auto_ban_only"
	TestModeNone        = "none"
)

func normalizeMode(m string) string {
	switch m = strings.ToLower(strings.TrimSpace(m)); m {
	case TestModeAll, TestModeAutoBanOnly, TestModeNone:
		return m
	}
	return ""
}

// EffectiveTestMode resolves the mode of one channel: its own test_mode, else
// auto_ban_only for a plan channel (a scheduled test would burn plan quota),
// else the global mode (default all).
func EffectiveTestMode(s dto.ChannelSettings, globalMode string) string {
	if m := normalizeMode(s.TestMode); m != "" {
		return m
	}
	if NormalizeKind(s.PlanKind) != "" {
		return TestModeAutoBanOnly
	}
	if m := normalizeMode(globalMode); m != "" {
		return m
	}
	return TestModeAll
}

// AllowScheduledTest reports whether the scheduled test may probe a channel
// with the given status. autoDisabled is status == auto-disabled. A channel
// whose plan has expired is never tested: a passing probe would re-enable it.
func AllowScheduledTest(s dto.ChannelSettings, globalMode string, autoDisabled bool, now time.Time) bool {
	if s.ExpiresAt > 0 && s.ExpiresAt <= now.Unix() {
		return false
	}
	switch EffectiveTestMode(s, globalMode) {
	case TestModeNone:
		return false
	case TestModeAutoBanOnly:
		return autoDisabled
	}
	return true
}
