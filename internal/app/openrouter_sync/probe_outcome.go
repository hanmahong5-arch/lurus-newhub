package openrouter_sync

import (
	"strings"

	"github.com/LurusTech/lurus-hub/internal/app/modelprobe"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// FailureKind classifies one failed probe of an OpenRouter free model. The
// distinction matters because the three failure families must be treated
// differently by the pool:
//
//   - FailureRegionBlocked: the provider refuses THIS model from our egress
//     country. A property of the model, so it leaves the offered pool until a
//     later probe succeeds.
//   - FailureKeyRejected: the credential is bad. A property of the channel
//     key, never a per-model block; flagging models for it would empty the
//     pool when a key rotates.
//   - FailureRateLimited: free-tier quota. Transient; availability is kept.
type FailureKind string

const (
	FailureNone          FailureKind = ""
	FailureRegionBlocked FailureKind = "region_blocked"
	FailureKeyRejected   FailureKind = "key_rejected"
	FailureRateLimited   FailureKind = "rate_limited"
	FailureOther         FailureKind = "other"
)

// Reasons surfaced to admins next to a model's probe state.
const (
	ReasonRegionBlocked = "region_blocked"
	ReasonRateLimited   = "rate_limited"
	ReasonProbeFailed   = "probe_failed"
)

var rateLimitMarkers = []string{
	"rate limit",
	"rate-limit",
	"ratelimit",
	"too many requests",
	"quota exceeded",
	"quota exhausted",
}

var keyMarkers = []string{
	"no auth credentials",
	"invalid api key",
	"invalid credentials",
	"unauthorized",
}

func containsAny(lower string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// ClassifyProbeFailure classifies a failed probe from the upstream HTTP
// status (0 when unknown, e.g. when only stored error text is available) and
// the error text / body.
func ClassifyProbeFailure(status int, text string) FailureKind {
	lower := strings.ToLower(text)

	// A region refusal is a 403 by contract; accept an unknown status too
	// because stored probe text carries no status. Any other status with the
	// same wording is not a geo gate.
	if modelprobe.IsRegionRefusal(status, text) {
		return FailureRegionBlocked
	}
	if status == 429 || containsAny(lower, rateLimitMarkers) {
		return FailureRateLimited
	}
	if status == 401 || status == 403 || containsAny(lower, keyMarkers) {
		return FailureKeyRejected
	}
	if status == 0 && strings.TrimSpace(text) == "" {
		return FailureNone
	}
	return FailureOther
}

// IsRegionBlocked reports whether a model's latest probe row says the model
// is refused for regional reasons. A healthy row never is: the prober clears
// last_error on success, and a stale text must not outlive a recovery.
func IsRegionBlocked(h entity.ModelHealth) bool {
	if h.Ok {
		return false
	}
	return ClassifyProbeFailure(0, h.LastError) == FailureRegionBlocked
}

// ReasonForHealth names why a probed model is not fully healthy, for the
// admin view: "" when healthy or never probed, otherwise one of the Reason*
// constants. A rate-limited model is still offered; the reason is shown only
// so the operator can see why its last probe failed.
func ReasonForHealth(h entity.ModelHealth) string {
	if h.Ok {
		return ""
	}
	switch ClassifyProbeFailure(0, h.LastError) {
	case FailureRegionBlocked:
		return ReasonRegionBlocked
	case FailureRateLimited:
		return ReasonRateLimited
	case FailureNone:
		if h.AutoDisabled {
			return ReasonProbeFailed
		}
		return ""
	default:
		return ReasonProbeFailed
	}
}
