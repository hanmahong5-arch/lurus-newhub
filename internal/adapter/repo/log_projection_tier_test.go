package repo

// log_projection_tier_test.go — the strip list and the governance
// classification are two hand-maintained lists describing the same policy, and
// nothing connected them.
//
// That gap is how `frt` (time to first token) spent its whole life hidden from
// the people who pay for the latency it measures: the classification framework
// puts latency under Activity/Public and already classes total_latency_ms as
// TierPublic, but frt was listed as Internal and, separately, named in the
// strip list. Two lists, one policy, no cross-check.
//
// This test is the cross-check. It is deliberately one-directional: the strip
// list is allowed to name keys the classification map has never heard of (it
// covers many per-modality ratio keys), but it may never strip a key the
// classification map calls Public — that combination is always a contradiction,
// and it is always the user-visible half that loses.

import (
	"encoding/json"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
)

func TestInternalOtherKeys_NoPublicField(t *testing.T) {
	for _, key := range internalOtherKeys {
		tier, known := governance.FieldClassification[key]
		if !known {
			// Fine: the strip list is broader than the classification map.
			continue
		}
		if tier == governance.TierPublic {
			t.Errorf("%q is stripped from user-facing logs but classified TierPublic. "+
				"One of the two is wrong, and while they disagree the user simply loses the field — "+
				"decide the policy in governance/classification.go, then make this list follow it.", key)
		}
	}
}

// TestSanitizeOtherForUser_KeepsLatencyStripsPricing states the resulting
// contract in terms of an actual payload: the caller keeps their own timing,
// and never sees what it cost us.
func TestSanitizeOtherForUser_KeepsLatencyStripsPricing(t *testing.T) {
	const payload = `{"frt":118,"cache_tokens":3456,"model_ratio":0.135,"group_ratio":1,"admin_info":{"use_channel":["25"]}}`

	got := SanitizeOtherForUser(payload)

	for _, keep := range []string{"frt", "cache_tokens"} {
		if !containsKey(t, got, keep) {
			t.Errorf("user projection dropped %q — %s", keep,
				map[string]string{
					"frt":          "time to first token is the caller's own request timing and the headline latency metric of a gateway",
					"cache_tokens": "the caller needs their cache hit count to reconcile a discounted charge",
				}[keep])
		}
	}
	for _, strip := range []string{"model_ratio", "group_ratio", "admin_info"} {
		if containsKey(t, got, strip) {
			t.Errorf("user projection leaked %q — that is our pricing/routing, not the caller's data", strip)
		}
	}
}

func containsKey(t *testing.T, jsonStr, key string) bool {
	t.Helper()
	m := map[string]any{}
	if jsonStr == "" {
		return false
	}
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Fatalf("projection is not valid JSON (%v): %s", err, jsonStr)
	}
	_, ok := m[key]
	return ok
}
