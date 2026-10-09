// Package planquota tracks monthly-plan (coding plan) channel accounts: it
// probes the vendor's own quota endpoint with the account key (read-only),
// parks a channel before a rolling window runs dry, classifies "window used
// up" upstream errors as cooldowns instead of bans, auto-pauses expired plans
// and decides which channels a scheduled test may touch.
//
// Parsing logic and the 403 wording are adapted from
// github.com/Wei-Shaw/sub2api (LGPL-3.0):
// backend/internal/service/cn_provider_quota_service.go and
// backend/internal/service/ratelimit_cn_providers.go. See THIRD_PARTY_NOTICES.md.
package planquota

import "strings"

// Plan kinds a channel may declare in its setting JSON (plan_kind).
const (
	KindNone        = "none"
	KindZhipuCoding = "zhipu_coding"
	KindKimiCoding  = "kimi_coding"
	KindMiniMax     = "minimax"
)

// NormalizeKind returns the canonical plan kind, or "" when the channel does
// not declare a supported plan (including "none" and unknown values).
func NormalizeKind(k string) string {
	switch k = strings.ToLower(strings.TrimSpace(k)); k {
	case KindZhipuCoding, KindKimiCoding, KindMiniMax:
		return k
	}
	return ""
}

// Window names.
const (
	Window5h     = "5h"
	WindowWeekly = "weekly"
)

// Window is one rolling quota window. ResetAt is unix seconds, 0 = unknown.
type Window struct {
	Name    string  `json:"name"`
	UsedPct float64 `json:"used_pct"`
	ResetAt int64   `json:"reset_at,omitempty"`
}

// Snapshot is the last probe result of one channel.
type Snapshot struct {
	ChannelID int      `json:"channel_id"`
	Kind      string   `json:"kind"`
	Windows   []Window `json:"windows,omitempty"`
	FetchedAt int64    `json:"fetched_at"`
	Error     string   `json:"error,omitempty"`
	// CooledUntil is the deadline this package parked the channel to because of
	// a threshold hit (0 = not parked by us); used to lift the park early when a
	// later probe shows the window recovered.
	CooledUntil int64 `json:"cooled_until,omitempty"`
	// BalanceLow marks a recoverable out-of-balance upstream answer.
	BalanceLow bool `json:"balance_low,omitempty"`
}
