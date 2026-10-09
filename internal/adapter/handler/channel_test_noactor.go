package handler

import "github.com/LurusTech/lurus-hub/internal/adapter/repo"

// testChannel is the no-actor form of the manual probe, kept for
// context_tier_channel_test_test.go's direct call. It is probeChannel with
// RecordConsumeLog on — the one probe path this cycle's plan keeps writing a
// consume-log row — and no attribution, so its row lands on user 1 the way it
// always did. The HTTP handler (TestChannel in channel-test.go) uses testChannelForActor
// instead.
func testChannel(channel *repo.Channel, testModel string, endpointType string) testResult {
	return probeChannel(channel, testModel, endpointType, channelProbeOptions{RecordConsumeLog: true})
}
