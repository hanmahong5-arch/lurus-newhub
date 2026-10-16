package modelprobe

import (
	"testing"
	"time"
)

const regionBody = `{"error":{"message":"This model is not available in your region.","code":403,"metadata":{"failed_routing_step":"Gate Endpoints with Geo Restrictions"}}}`

func TestIsRegionRefusal(t *testing.T) {
	cases := []struct {
		name   string
		status int
		text   string
		want   bool
	}{
		{"region 403", 403, regionBody, true},
		{"stored text, status unknown", 0, "This model is not available in your region.", true},
		{"routing step only", 403, "failed at Gate Endpoints with Geo Restrictions", true},
		{"bad key 403", 403, `{"error":{"message":"Invalid API key"}}`, false},
		{"same words on a 500", 500, regionBody, false},
		{"plain failure", 502, "upstream error", false},
	}
	for _, tc := range cases {
		if got := IsRegionRefusal(tc.status, tc.text); got != tc.want {
			t.Errorf("%s: IsRegionRefusal = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A region refusal leaves routing on the FIRST probe: every later probe from
// the same egress is refused the same way, so waiting for the threshold only
// keeps a dead model routable for threshold-1 more probe rounds.
func TestApply_RegionRefusalDisablesOnFirstHit(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	next, transition := Apply(nil, Result{StatusCode: 403, Err: regionBody}, now, threshold)
	if !next.AutoDisabled || transition != "disabled" {
		t.Fatalf("got AutoDisabled=%v transition=%q, want disabled on the first region refusal", next.AutoDisabled, transition)
	}
	if next.AutoDisabledAt != now.Unix() || next.ConsecutiveFailures != 1 {
		t.Errorf("row not stamped: %+v", next)
	}
}

// A bad-key 403 keeps the old threshold behaviour: one failure is not enough.
func TestApply_KeyRejection403StillWaitsForThreshold(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	next, transition := Apply(nil, Result{StatusCode: 403, Err: `{"error":{"message":"Invalid API key"}}`}, now, threshold)
	if next.AutoDisabled || transition != "" {
		t.Fatalf("got AutoDisabled=%v transition=%q, want no disable after one key failure", next.AutoDisabled, transition)
	}
}
