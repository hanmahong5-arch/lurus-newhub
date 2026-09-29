package handler

// model_probe_wiring_test.go — modelProbeFunc must correctly translate
// probeChannel's testResult into modelprobe.Result. Exercised through the
// unsupported-channel-type branch of probeChannel (channel-test.go), which
// returns immediately with only localErr set and touches neither the
// database nor the network — the one deterministic, hermetic path through
// probeChannel available without a live upstream.

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestModelProbeFunc_UnsupportedChannelTypeIsAFailureWithErrText(t *testing.T) {
	channel := &repo.Channel{Id: 1, Type: constant.ChannelTypeMidjourney}

	res := modelProbeFunc(context.Background(), channel, "any-model")

	if res.OK {
		t.Error("OK = true, want false for an unsupported channel type")
	}
	if res.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0 (probeChannel never produced a newAPIError on this path)", res.StatusCode)
	}
	if res.Err == "" {
		t.Error("Err must not be empty")
	}
	if res.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d, want >= 0", res.LatencyMs)
	}
}
