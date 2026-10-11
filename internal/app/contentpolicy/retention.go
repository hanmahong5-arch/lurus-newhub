// Package contentpolicy is the relay data-control core: how much of a request
// is retained in logs (retention modes) and which request-body content is
// masked or refused before it reaches an upstream (content rules).
//
// The package is pure (no DB, no gin): the repo layer persists settings and
// the relay entry calls Guard; keeping the decisions here makes every layer
// testable without infrastructure.
//
// Design inspired by the layered disable_content_logging switch of
// maximhq/bifrost (Apache-2.0) and the observe/enforce guardrail split of
// Portkey-AI/gateway (MIT); no source was copied.
package contentpolicy

import (
	"os"
	"strings"
	"sync/atomic"
)

// RetentionMode is how much request/response content a log row may keep.
type RetentionMode string

const (
	// RetentionInherit ("") means "no opinion at this layer"; the layers
	// above decide. It is the only legal value in a column default.
	RetentionInherit RetentionMode = ""
	// RetentionFull keeps today's behaviour.
	RetentionFull RetentionMode = "full"
	// RetentionMetadataOnly keeps usage, cost, latency, model and status and
	// drops every free-text field.
	RetentionMetadataOnly RetentionMode = "metadata_only"
	// RetentionNone is metadata_only plus the request-derived identifiers
	// (fingerprint, client IP) and every non-numeric detail field.
	RetentionNone RetentionMode = "none"
)

// strictness orders the modes; a larger value is stricter.
func strictness(m RetentionMode) int {
	switch m {
	case RetentionNone:
		return 3
	case RetentionMetadataOnly:
		return 2
	case RetentionFull:
		return 1
	default:
		return 0
	}
}

// ValidRetention reports whether s is a storable setting ("" included).
func ValidRetention(s string) bool {
	switch RetentionMode(s) {
	case RetentionInherit, RetentionFull, RetentionMetadataOnly, RetentionNone:
		return true
	}
	return false
}

// Stricter returns the stricter of a and b. An unknown value is treated as
// "inherit" so a corrupt row can never loosen a decision.
func Stricter(a, b RetentionMode) RetentionMode {
	if strictness(b) > strictness(a) {
		return b
	}
	return a
}

// Looser reports whether candidate would loosen relative to floor, i.e. the
// candidate is a concrete mode that is less strict than floor. Inherit never
// loosens (the floor still applies).
func Looser(candidate, floor RetentionMode) bool {
	if candidate == RetentionInherit {
		return false
	}
	return strictness(candidate) < strictness(floor)
}

// Resolve folds platform default -> tenant -> token into the effective mode.
// The strictest layer wins, so a token can never relax its tenant and a
// tenant can never relax the platform: this is the whole point of resolving
// by max() instead of "most specific layer overrides". The empty result is
// reported as full (nothing restricts retention).
func Resolve(platform, tenant, token RetentionMode) RetentionMode {
	out := Stricter(Stricter(RetentionInherit, platform), Stricter(tenant, token))
	if out == RetentionInherit {
		return RetentionFull
	}
	return out
}

var platformOverride atomic.Pointer[string]

// SetPlatformDefault overrides the platform default (tests / admin wiring).
// nil restores the environment-derived value.
func SetPlatformDefault(m *RetentionMode) {
	if m == nil {
		platformOverride.Store(nil)
		return
	}
	s := string(*m)
	platformOverride.Store(&s)
}

// PlatformDefault is the platform-wide floor: CONTENT_RETENTION_DEFAULT, or
// full when unset/invalid.
func PlatformDefault() RetentionMode {
	if p := platformOverride.Load(); p != nil {
		return RetentionMode(*p)
	}
	v := strings.TrimSpace(os.Getenv("CONTENT_RETENTION_DEFAULT"))
	if ValidRetention(v) {
		return RetentionMode(v)
	}
	return RetentionFull
}

// LogRecord is the content-bearing slice of a log row that retention trims.
// The repo layer builds one per write and copies the result back, so the
// trimming rules live in exactly one place.
type LogRecord struct {
	Content            string
	Other              map[string]interface{}
	Ip                 string
	RequestFingerprint string
}

// otherStringKeysKept lists the Other keys whose string value is a
// machine-generated identifier, never user content.
var otherStringKeysKept = map[string]bool{
	"request_id": true, "session_id": true, "end_user": true,
	"source_product": true, "upstream_request_id": true,
	"error_type": true, "error_code": true, "settlement": true,
	"data_flow_dest": true, "reasoning_effort": true, "request_path": true,
	"upstream_model": true, "upstream_model_name": true, "source": true,
	"status_reason": true, "channel_name": true, "data_flow_source": true,
}

// otherStringKeysKeptNone is the subset kept under RetentionNone.
var otherStringKeysKeptNone = map[string]bool{
	"request_id": true, "error_type": true, "error_code": true,
	"settlement": true, "source_product": true,
}

// ApplyRetention trims r in place for mode. Full is a no-op. Usage, cost,
// latency, model and status are columns outside LogRecord and are never
// touched, so billing reconciliation survives every mode.
func ApplyRetention(mode RetentionMode, r *LogRecord) {
	if r == nil || strictness(mode) < strictness(RetentionMetadataOnly) {
		return
	}
	r.Content = ""
	keep := otherStringKeysKept
	if mode == RetentionNone {
		keep = otherStringKeysKeptNone
		r.Ip = ""
		r.RequestFingerprint = ""
	}
	r.Other = scrubOther(r.Other, keep)
}

// scrubOther returns a copy of m keeping numbers and booleans, and strings
// only under allow-listed keys. Nested maps/slices are scrubbed recursively,
// with the same rule applied to their keys (slice elements inherit the key of
// the slice). An allow-list, not a deny-list: a new free-text key added later
// is dropped by default instead of leaking.
func scrubOther(m map[string]interface{}, keep map[string]bool) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if sv, ok := scrubValue(k, v, keep); ok {
			out[k] = sv
		}
	}
	return out
}

func scrubValue(key string, v interface{}, keep map[string]bool) (interface{}, bool) {
	switch t := v.(type) {
	case nil:
		return nil, false
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return t, true
	case string:
		if keep[key] {
			return t, true
		}
		return nil, false
	case map[string]interface{}:
		return scrubOther(t, keep), true
	case []interface{}:
		out := make([]interface{}, 0, len(t))
		for _, e := range t {
			if sv, ok := scrubValue(key, e, keep); ok {
				out = append(out, sv)
			}
		}
		return out, true
	case []string:
		if !keep[key] && key != "use_channel" {
			return nil, false
		}
		return t, true
	default:
		return nil, false
	}
}
