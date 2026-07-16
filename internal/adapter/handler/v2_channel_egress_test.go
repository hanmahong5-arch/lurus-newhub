package handler

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// TestValidateChannelEgress covers the single SSRF gate that CreateChannelV2,
// UpdateChannelV2, TestChannelV2 and FetchUpstreamModelsV2 all funnel through.
// Host cases use IP literals so the test is hermetic (no DNS).
func TestValidateChannelEgress(t *testing.T) {
	fs := system_setting.GetFetchSetting()
	prev := *fs
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	t.Cleanup(func() { *fs = prev })

	strPtr := func(s string) *string { return &s }

	// Internal base_url must be rejected.
	if err := validateChannelEgress(&repo.Channel{BaseURL: strPtr("http://10.0.0.5")}); err == nil {
		t.Error("channel with internal base_url should be rejected")
	}
	// Internal proxy must be rejected even with a public base_url.
	internalProxy := `{"proxy":"socks5://192.168.1.1:8080"}`
	if err := validateChannelEgress(&repo.Channel{
		BaseURL: strPtr("https://8.8.8.8"),
		Setting: strPtr(internalProxy),
	}); err == nil {
		t.Error("channel with internal proxy should be rejected")
	}
	// Public base_url with no proxy passes.
	if err := validateChannelEgress(&repo.Channel{BaseURL: strPtr("https://8.8.8.8")}); err != nil {
		t.Errorf("channel with public base_url should pass, got %v", err)
	}
}
