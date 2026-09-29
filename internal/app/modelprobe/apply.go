package modelprobe

import (
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// lastErrorMaxLen bounds entity.ModelHealth.LastError so a chatty upstream
// error body cannot grow the row unbounded.
const lastErrorMaxLen = 500

// rateLimitPhrases are checked case-insensitively (with "_" normalised to a
// space) against Result.Err. A free-tier pool running out of its daily quota
// is not a broken model — Apply must not count it toward
// ConsecutiveFailures or auto-disable on it.
var rateLimitPhrases = []string{
	"rate limit",
	"ratelimit",
	"too many requests",
	"quota exhausted",
	"quota exceeded",
	"insufficient quota",
}

// isRateLimited reports whether res is a rate-limit / quota-exhaustion
// failure rather than a genuinely broken model: StatusCode 429, or Err text
// that clearly says so.
func isRateLimited(res Result) bool {
	if res.StatusCode == 429 {
		return true
	}
	if res.Err == "" {
		return false
	}
	normalized := strings.ToLower(strings.ReplaceAll(res.Err, "_", " "))
	for _, phrase := range rateLimitPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func truncateLastError(err string) string {
	if len(err) <= lastErrorMaxLen {
		return err
	}
	return err[:lastErrorMaxLen]
}

// Apply turns one probe Result into the next entity.ModelHealth row plus a
// transition label ("", "disabled" or "recovered") for the caller to act on
// (routing cache / ability rows). prev is nil for a pair that has never been
// probed before. Apply does not set ChannelId/Model on the returned row —
// the caller (RunOnce) owns identifying which pair this is.
func Apply(prev *entity.ModelHealth, res Result, now time.Time, threshold int) (entity.ModelHealth, string) {
	var next entity.ModelHealth
	if prev != nil {
		next = *prev
	}
	next.LastProbeAt = now.Unix()
	next.LatencyMs = res.LatencyMs
	next.UpdatedAt = now.Unix()

	if res.OK {
		next.Ok = true
		next.ConsecutiveFailures = 0
		next.LastError = ""
		transition := ""
		if next.AutoDisabled {
			next.AutoDisabled = false
			next.AutoDisabledAt = 0
			transition = "recovered"
		}
		return next, transition
	}

	next.Ok = false
	next.LastError = truncateLastError(res.Err)

	if isRateLimited(res) {
		// Quota exhaustion, not a broken model: record the failure but leave
		// the streak and disable state untouched.
		return next, ""
	}

	next.ConsecutiveFailures++
	transition := ""
	if next.ConsecutiveFailures >= threshold && !next.AutoDisabled {
		next.AutoDisabled = true
		next.AutoDisabledAt = now.Unix()
		transition = "disabled"
	}
	return next, transition
}
