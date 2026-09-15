package app

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// TestGenerateTextOtherInfo_ConversionDropped drives GenerateTextOtherInfo
// directly (rather than through a converter) to pin the funnel contract the
// L5 spec relies on: compatible_handler.go:490 calls this function and the
// Claude/audio/wss generators (log_info_generate.go) all wrap it, so stashing
// RelayInfo.ConversionDropped is enough to reach every success-path log row
// with no edit to any of those call sites.
func TestGenerateTextOtherInfo_ConversionDropped(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ConversionDropped: []string{"tool_choice", "top_k"},
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}
	other := GenerateTextOtherInfo(createTestGinContext(), info, 1, 1, 1, 0, 0, 0, 1)

	got, ok := other["conversion_dropped"]
	if !ok {
		t.Fatal(`other["conversion_dropped"] missing, want the RelayInfo value projected through`)
	}
	names, ok := got.([]string)
	if !ok || len(names) != 2 || names[0] != "tool_choice" || names[1] != "top_k" {
		t.Errorf("conversion_dropped = %#v, want [tool_choice top_k]", got)
	}
}

// TestGenerateTextOtherInfo_NoConversionDroppedKeyWhenEmpty proves the
// omission half of the contract: a nil RelayInfo.ConversionDropped (the
// common case — nothing was dropped, or this request never went through a
// cross-wire converter) must not write an always-present empty key.
func TestGenerateTextOtherInfo_NoConversionDroppedKeyWhenEmpty(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	other := GenerateTextOtherInfo(createTestGinContext(), info, 1, 1, 1, 0, 0, 0, 1)

	if _, ok := other["conversion_dropped"]; ok {
		t.Errorf(`other["conversion_dropped"] present with nil ConversionDropped, want key absent`)
	}
}
