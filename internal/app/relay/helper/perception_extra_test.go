package helper

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func TestEstimateQuotaFromUsage_CacheImageCreationTokens(t *testing.T) {
	// OpenAI-wire usage (PromptTokensIncludeCached, stamped at the parse site
	// since 2026-08-29 — the deduction keys on the flag, not ChannelType):
	// cached/image/creation tokens are subtracted from the base and re-added
	// with their own ratios.
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		PriceData: types.PriceData{
			ModelRatio:         1.0,
			CompletionRatio:    1.0,
			CacheRatio:         0.5,
			ImageRatio:         2.0,
			CacheCreationRatio: 3.0,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}
	usage := &dto.Usage{
		PromptTokens:              100,
		CompletionTokens:          10,
		PromptTokensIncludeCached: true,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         20,
			ImageTokens:          10,
			CachedCreationTokens: 5,
		},
	}
	// base = 100 - 20 - 5 - 10 = 65
	// cached = 20*0.5 = 10 ; image = 10*2 = 20 ; creation = 5*3 = 15
	// prompt = 65 + 10 + 20 + 15 = 110 ; completion = 10*1 = 10
	// total = (110 + 10) * (1*1) = 120
	got := EstimateQuotaFromUsage(info, usage)
	if got != 120 {
		t.Errorf("EstimateQuotaFromUsage = %d, want 120", got)
	}
}

func TestEstimateQuotaFromUsage_AnthropicKeepsCacheInBase(t *testing.T) {
	// Anthropic channel: cached tokens are NOT subtracted from base.
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
		PriceData: types.PriceData{
			ModelRatio:     1.0,
			CacheRatio:     0.5,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}
	usage := &dto.Usage{
		PromptTokens:        100,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 20},
	}
	// base stays 100 (anthropic), cached adds 20*0.5=10 -> 110
	got := EstimateQuotaFromUsage(info, usage)
	if got != 110 {
		t.Errorf("EstimateQuotaFromUsage = %d, want 110", got)
	}
}

func TestEstimateQuotaFromUsage_FixedPriceClampedToOne(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			UsePrice:       true,
			ModelPrice:     0, // 0 * anything rounds to 0 -> clamped to 1
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}
	got := EstimateQuotaFromUsage(info, &dto.Usage{})
	if got != 1 {
		t.Errorf("EstimateQuotaFromUsage = %d, want 1 (clamped)", got)
	}
}

func TestEstimateQuotaFromUsage_NonzeroRatioClampedToOne(t *testing.T) {
	// tiny non-zero ratio with zero tokens -> quota rounds to 0 but ratio!=0 -> clamp 1
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			ModelRatio:     0.001,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}
	got := EstimateQuotaFromUsage(info, &dto.Usage{})
	if got != 1 {
		t.Errorf("EstimateQuotaFromUsage = %d, want 1 (clamped)", got)
	}
}

func TestSetPerceptionHeaders(t *testing.T) {
	t.Run("nil guards", func(t *testing.T) {
		SetPerceptionHeaders(nil, &relaycommon.RelayInfo{}, nil) // must not panic
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		SetPerceptionHeaders(c, nil, nil) // must not panic
	})

	t.Run("writes cost/provider/request-id headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)
		c.Set(common.RequestIdKey, "rid-7")
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		}
		ext := &types.LurusUsageExtension{CostLB: 1.25, BalanceRemaining: 8.5}
		SetPerceptionHeaders(c, info, ext)
		if w.Header().Get("X-Request-Cost") != "1.25" {
			t.Errorf("cost header = %q, want 1.25", w.Header().Get("X-Request-Cost"))
		}
		if w.Header().Get("X-Quota-Remaining") != "8.5" {
			t.Errorf("remaining header = %q, want 8.5", w.Header().Get("X-Quota-Remaining"))
		}
		if w.Header().Get("X-Model-Provider") == "" {
			t.Error("provider header should be set")
		}
		if w.Header().Get("X-Request-Id") != "rid-7" {
			t.Errorf("request-id header = %q", w.Header().Get("X-Request-Id"))
		}
	})

	t.Run("nil ext omits cost headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)
		SetPerceptionHeaders(c, &relaycommon.RelayInfo{}, nil)
		if w.Header().Get("X-Request-Cost") != "" {
			t.Error("cost header should be absent with nil ext")
		}
	})
}

func TestSetRateLimitHeaders(t *testing.T) {
	t.Run("nil context no panic", func(t *testing.T) {
		SetRateLimitHeaders(nil, 1, 2, 3)
	})

	t.Run("writes rate-limit headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		SetRateLimitHeaders(c, 100, 42, 30)
		if w.Header().Get("Retry-After") != "30" {
			t.Errorf("retry-after = %q", w.Header().Get("Retry-After"))
		}
		if w.Header().Get("X-RateLimit-Limit") != "100" {
			t.Errorf("limit = %q", w.Header().Get("X-RateLimit-Limit"))
		}
		if w.Header().Get("X-RateLimit-Remaining") != "42" {
			t.Errorf("remaining = %q", w.Header().Get("X-RateLimit-Remaining"))
		}
	})
}

func TestPerceptionFormatFloat(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{1.5, "1.5"},
		{0, "0"},
		{1.25, "1.25"},
		{100, "100"},
	}
	for _, tt := range tests {
		if got := perceptionFormatFloat(tt.in); got != tt.want {
			t.Errorf("perceptionFormatFloat(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
