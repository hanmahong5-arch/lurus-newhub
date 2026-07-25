package app

// Proves the routing trace reaches the log row's other_info payload — the
// recording tests in route_attempts_test.go would pass even if nothing ever
// read the context back out.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// relayInfoForOtherInfo mirrors what the relay has populated by settlement
// time; GenerateTextOtherInfo dereferences ChannelMeta unconditionally.
func relayInfoForOtherInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
}

func adminInfoFrom(t *testing.T, other map[string]interface{}) map[string]interface{} {
	t.Helper()
	admin, ok := other["admin_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("admin_info missing or wrong type: %#v", other["admin_info"])
	}
	return admin
}

func TestGenerateTextOtherInfo_RouteAttemptsAttachedWhenRequestBounced(t *testing.T) {
	c := createTestGinContext()
	RecordRouteAttempt(c, RouteAttempt{
		ChannelID: 1, ChannelName: "primary", Provider: "openai",
		Outcome: RouteAttemptOutcomeUpstreamErr, ErrorCode: "channel:timeout", StatusCode: 504, DurationMs: 30000,
	})
	RecordRouteAttempt(c, RouteAttempt{
		ChannelID: 2, ChannelName: "backup", Provider: "openai",
		Outcome: RouteAttemptOutcomeSuccess, DurationMs: 900,
	})

	other := GenerateTextOtherInfo(c, relayInfoForOtherInfo(), 1.0, 1.0, 1.0, 0, 0, 0, 1.0)
	attempts, ok := adminInfoFrom(t, other)["route_attempts"].([]RouteAttempt)
	if !ok {
		t.Fatalf("route_attempts missing from admin_info: %#v", other["admin_info"])
	}
	if len(attempts) != 2 {
		t.Fatalf("expected both attempts, got %d: %+v", len(attempts), attempts)
	}
	// The whole point is that the ABANDONED channel survives into the record.
	if attempts[0].ChannelID != 1 || attempts[0].ErrorCode != "channel:timeout" || attempts[0].StatusCode != 504 {
		t.Errorf("failed attempt not preserved: %+v", attempts[0])
	}
	if attempts[1].ChannelID != 2 || attempts[1].Outcome != RouteAttemptOutcomeSuccess {
		t.Errorf("serving attempt not preserved: %+v", attempts[1])
	}
}

func TestGenerateTextOtherInfo_NoRouteAttemptsOnSingleAttempt(t *testing.T) {
	c := createTestGinContext()
	RecordRouteAttempt(c, RouteAttempt{ChannelID: 1, Outcome: RouteAttemptOutcomeSuccess, DurationMs: 120})

	other := GenerateTextOtherInfo(c, relayInfoForOtherInfo(), 1.0, 1.0, 1.0, 0, 0, 0, 1.0)
	if _, present := adminInfoFrom(t, other)["route_attempts"]; present {
		t.Error("a request served on the first try must not carry a routing trace")
	}
}

func TestGenerateTextOtherInfo_NoRouteAttemptsWhenNoneRecorded(t *testing.T) {
	c := createTestGinContext()
	other := GenerateTextOtherInfo(c, relayInfoForOtherInfo(), 1.0, 1.0, 1.0, 0, 0, 0, 1.0)
	if _, present := adminInfoFrom(t, other)["route_attempts"]; present {
		t.Error("no attempts recorded must mean no route_attempts key")
	}
}
