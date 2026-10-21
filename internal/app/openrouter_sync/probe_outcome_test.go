package openrouter_sync

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// Recorded shapes of OpenRouter probe failures measured from a mainland
// egress. The region-block body is the one the provider returns for whole
// provider families; the rate-limit and bad-key bodies are the other two
// failures a probe sees on a free pool.
const (
	bodyRegionBlockJSON = `{"error":{"message":"This model is not available in your region.","code":403,"metadata":{"failed_routing_step":"Gate Endpoints with Geo Restrictions"}}}`
	bodyRegionMsgOnly   = "This model is not available in your region."
	bodyRoutingStepOnly = `{"error":{"code":403,"metadata":{"failed_routing_step":"Gate Endpoints with Geo Restrictions"}}}`
	bodyRateLimited     = `{"error":{"message":"model-a:free is temporarily rate-limited upstream","code":429}}`
	bodyBadKey          = `{"error":{"message":"No auth credentials found","code":401}}`
	bodyForbiddenKey    = `{"error":{"message":"Your API key has been disabled","code":403}}`
)

func TestClassifyProbeFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
		text   string
		want   FailureKind
	}{
		{"region block json 403", 403, bodyRegionBlockJSON, FailureRegionBlocked},
		{"region block message only 403", 403, bodyRegionMsgOnly, FailureRegionBlocked},
		{"region block routing step only 403", 403, bodyRoutingStepOnly, FailureRegionBlocked},
		{"region block, status unknown (stored text)", 0, bodyRegionMsgOnly, FailureRegionBlocked},
		{"region wording on a 500 is not a region block", 500, bodyRegionMsgOnly, FailureOther},
		{"403 without marker is a key problem, never a per-model block", 403, bodyForbiddenKey, FailureKeyRejected},
		{"bare 403 is a key problem", 403, "", FailureKeyRejected},
		{"401 is a key problem", 401, bodyBadKey, FailureKeyRejected},
		{"429 is transient", 429, bodyRateLimited, FailureRateLimited},
		{"rate-limit wording without status", 0, bodyRateLimited, FailureRateLimited},
		{"503 overload is just a failure", 503, "Service is too busy", FailureOther},
		{"empty", 0, "", FailureNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyProbeFailure(tc.status, tc.text); got != tc.want {
				t.Fatalf("ClassifyProbeFailure(%d, %q) = %q, want %q", tc.status, tc.text, got, tc.want)
			}
		})
	}
}

func TestReasonForHealth(t *testing.T) {
	cases := []struct {
		name string
		h    entity.ModelHealth
		want string
	}{
		{"healthy", entity.ModelHealth{Ok: true}, ""},
		{"healthy ignores stale text", entity.ModelHealth{Ok: true, LastError: bodyRegionMsgOnly}, ""},
		{"region blocked", entity.ModelHealth{LastError: bodyRegionBlockJSON}, ReasonRegionBlocked},
		{"region blocked and auto disabled", entity.ModelHealth{LastError: bodyRegionMsgOnly, AutoDisabled: true}, ReasonRegionBlocked},
		{"rate limited stays available", entity.ModelHealth{LastError: bodyRateLimited}, ReasonRateLimited},
		{"generic failure", entity.ModelHealth{LastError: "Service is too busy"}, ReasonProbeFailed},
		{"auto disabled without text", entity.ModelHealth{AutoDisabled: true}, ReasonProbeFailed},
		{"never probed", entity.ModelHealth{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReasonForHealth(tc.h); got != tc.want {
				t.Fatalf("ReasonForHealth(%+v) = %q, want %q", tc.h, got, tc.want)
			}
		})
	}
}

func TestIsRegionBlocked(t *testing.T) {
	if !IsRegionBlocked(entity.ModelHealth{LastError: bodyRegionBlockJSON}) {
		t.Fatal("region-block failure must be reported blocked")
	}
	if IsRegionBlocked(entity.ModelHealth{Ok: true, LastError: bodyRegionBlockJSON}) {
		t.Fatal("a healthy row is never blocked, even with stale text")
	}
	if IsRegionBlocked(entity.ModelHealth{LastError: bodyRateLimited}) {
		t.Fatal("429 must not read as a region block")
	}
}
