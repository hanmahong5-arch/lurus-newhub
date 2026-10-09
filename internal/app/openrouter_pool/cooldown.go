// Package openrouter_pool implements per-key cooldown tracking and
// auto-recovery for OpenRouter API key pools. It is invoked from the relay
// error path when a 429 is observed on an OpenRouter multi-key channel and
// from a master-only ticker that re-enables keys after their cooldown.
package openrouter_pool

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// Cooldown bounds. The lower bound prevents reaper "thrash" (re-enable then
// instantly hit 429 again); the upper bound caps the worst case so a clock
// skew or malformed reset header can never lock a key forever.
const (
	cooldownMin     = 30 * time.Second
	cooldownMax     = 24 * time.Hour
	cooldownFallbk  = 60 * time.Second
	cooldownDailyTL = 24 * time.Hour
)

// daily-limit signals — OpenRouter free models surface the daily quota in
// either the body's error message or its metadata.
var dailyKeywords = []string{
	"free-models-per-day",
	"free models per day",
	"daily limit",
	"per day",
}

// ParseCooldownUntil returns the Unix-second deadline at which a key may be
// re-enabled, given an upstream 429 response. It tries (in priority order):
//
//  1. respHeader["X-Ratelimit-Reset"] — interpreted as either ms-since-epoch,
//     s-since-epoch, or s-from-now, with a heuristic that picks the closest sane value.
//  2. body JSON: {"error":{"metadata":{"headers":{"X-RateLimit-Reset": "..."}}}}
//     (free-tier OpenRouter responses embed the upstream provider header here).
//  3. body keyword scan: "free-models-per-day"/"daily" → 24h cooldown
//     (OpenRouter channels only).
//  4. Fallback: 60s.
//
// The result is always clamped to [now+30s, now+24h]. The function never returns
// 0; a non-zero value lets the reaper distinguish "auto-recoverable" from
// "permanently disabled" entries.
func ParseCooldownUntil(respHeader http.Header, respBody []byte, now time.Time, channelType int) int64 {
	// 1. Header
	if respHeader != nil {
		if v := respHeader.Get("X-Ratelimit-Reset"); v != "" {
			if until, ok := interpretResetValue(v, now); ok {
				return clamp(until, now)
			}
		}
		// Some proxies forward as Retry-After (seconds-from-now).
		if v := respHeader.Get("Retry-After"); v != "" {
			if secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && secs > 0 {
				return clamp(now.Unix()+secs, now)
			}
		}
	}

	// 2. Body metadata
	if len(respBody) > 0 {
		if until, ok := extractFromBodyMetadata(respBody, now); ok {
			return clamp(until, now)
		}
	}

	// 3. Keyword fallback (24h). These keywords are an OpenRouter free-tier
	// convention, so any other upstream falls through to the 60s default.
	if channelType == constant.ChannelTypeOpenRouter && len(respBody) > 0 {
		lower := strings.ToLower(string(respBody))
		for _, kw := range dailyKeywords {
			if strings.Contains(lower, kw) {
				return clamp(now.Add(cooldownDailyTL).Unix(), now)
			}
		}
	}

	// 4. Fallback
	return clamp(now.Add(cooldownFallbk).Unix(), now)
}

// interpretResetValue tries three interpretations and picks the one closest to
// now (within [now+30s, now+24h]). Returns ok=false if none fit.
func interpretResetValue(raw string, now time.Time) (int64, bool) {
	raw = strings.TrimSpace(raw)
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// Try float, in case OpenRouter ever returns a decimal.
		f, ferr := strconv.ParseFloat(raw, 64)
		if ferr != nil {
			return 0, false
		}
		n = int64(f)
	}
	if n <= 0 {
		return 0, false
	}

	nowUnix := now.Unix()
	// Pick exactly one interpretation by magnitude (avoids the "is this
	// past-Unix-seconds or future-seconds-from-now" ambiguity that bites
	// when the raw value sits near nowUnix). Thresholds:
	//   n < 86400  (< 1d)  → seconds-from-now
	//   n < 10^12  (< year 33658 in seconds; OR before 1970-01-15 in ms) → Unix seconds
	//   else                                                              → Unix milliseconds
	var candidate int64
	switch {
	case n < 86_400:
		candidate = nowUnix + n
	case n < 1_000_000_000_000:
		candidate = n
	default:
		candidate = n / 1000
	}
	if candidate <= nowUnix {
		// Past timestamp → reject; caller will fall through to body scan / fallback.
		return 0, false
	}
	// We trust the chosen interpretation even if it's wildly far in the future;
	// the outer clamp() pins it to [now+30s, now+24h]. Returning it here lets
	// the caller distinguish "got a number we believe in" from "no signal".
	return candidate, true
}

// openRouterErrBody is a permissive shape that captures the few fields we care
// about from an OpenRouter error response. Unrecognized JSON shapes simply
// return zero — the keyword scan will still run.
type openRouterErrBody struct {
	Error struct {
		Message  string `json:"message"`
		Metadata struct {
			Headers map[string]string `json:"headers"`
		} `json:"metadata"`
	} `json:"error"`
}

func extractFromBodyMetadata(body []byte, now time.Time) (int64, bool) {
	var parsed openRouterErrBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, false
	}
	if h := parsed.Error.Metadata.Headers; h != nil {
		// Try common header names case-insensitively.
		for k, v := range h {
			lk := strings.ToLower(k)
			if lk == "x-ratelimit-reset" || lk == "x-rate-limit-reset" {
				if until, ok := interpretResetValue(v, now); ok {
					return until, true
				}
			}
			if lk == "retry-after" {
				if secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && secs > 0 {
					return now.Unix() + secs, true
				}
			}
		}
	}
	return 0, false
}

// clamp pins the candidate deadline into [now+30s, now+24h].
func clamp(until int64, now time.Time) int64 {
	low := now.Add(cooldownMin).Unix()
	high := now.Add(cooldownMax).Unix()
	if until < low {
		return low
	}
	if until > high {
		return high
	}
	return until
}
