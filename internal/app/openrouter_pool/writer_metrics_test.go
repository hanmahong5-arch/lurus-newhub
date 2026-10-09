package openrouter_pool

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func rateLimitCooldownCount(id int) float64 {
	return testutil.ToFloat64(metrics.ChannelCooldownTotal.WithLabelValues(strconv.Itoa(id), "rate_limit"))
}

func plain429() *types.NewAPIError {
	e := types.WithOpenAIError(types.OpenAIError{Message: "Rate limit reached for requests", Type: "rate_limit_error"}, 429)
	e.UpstreamHeader = http.Header{"Retry-After": []string{"60"}}
	return e
}

// Deleting either RecordChannelCooldown call in MaybeMarkCooldown must turn
// this red: single-key and multi-key cooldown writes each feed the series.
func TestMaybeMarkCooldown_CountsCooldownMetric(t *testing.T) {
	withAutoDisable(t)
	markSeam(t, true)

	single := baseChannelErr()
	single.ChannelType, single.IsMultiKey, single.ChannelId = constant.ChannelTypeOpenAI, false, 9301
	before := rateLimitCooldownCount(9301)
	MaybeMarkCooldown(single, plain429())
	if got := rateLimitCooldownCount(9301); got != before+1 {
		t.Errorf("single-key cooldown: counter %v -> %v, want +1", before, got)
	}

	multi := baseChannelErr()
	multi.ChannelType, multi.ChannelId, multi.AutoBan = constant.ChannelTypeOpenAI, 9302, true
	before = rateLimitCooldownCount(9302)
	MaybeMarkCooldown(multi, plain429())
	if got := rateLimitCooldownCount(9302); got != before+1 {
		t.Errorf("multi-key cooldown: counter %v -> %v, want +1", before, got)
	}
}

func TestMaybeMarkCooldown_RequestCaused429_NoCooldownMetric(t *testing.T) {
	withAutoDisable(t)
	markSeam(t, true)
	ce := baseChannelErr()
	ce.ChannelType, ce.IsMultiKey, ce.ChannelId = constant.ChannelTypeOpenAI, false, 9303
	before := rateLimitCooldownCount(9303)
	MaybeMarkCooldown(ce, requestTooLarge429())
	if got := rateLimitCooldownCount(9303); got != before {
		t.Errorf("request-caused 429 moved the cooldown counter %v -> %v", before, got)
	}
}
