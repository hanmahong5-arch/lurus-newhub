package app

// log_info_frt_test.go — `frt` (time to first token) must be absent, not
// negative, when no first token was ever observed.
//
// RelayInfo seeds FirstResponseTime to StartTime−1s as a "never happened"
// sentinel and only the streaming adaptors call SetFirstResponseTime. The
// unconditional subtraction therefore wrote a literal -1000 into every
// NON-streaming consume log. Measured on UAT 2026-09-01: every non-stream row
// carried frt=-1000, and the console's latency panel rendered "-1000ms" as the
// time to first token. It was invisible to customers only because `frt` is
// classified TierInternal and stripped for non-admins — i.e. the field has been
// wrong for as long as it has existed, and the only reason nobody complained is
// that nobody who cares could see it.
//
// Mutation oracle: drop the HasSendResponse guard and the sentinel case below
// goes red with the exact -1000 it used to record.

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

func frtOf(t *testing.T, info *relaycommon.RelayInfo) (float64, bool) {
	t.Helper()
	other := GenerateTextOtherInfo(createTestGinContext(), info, 1, 1, 1, 0, 0, 0, 0)
	v, ok := other["frt"]
	if !ok {
		return 0, false
	}
	f, isFloat := v.(float64)
	if !isFloat {
		t.Fatalf("frt is %T, want float64", v)
	}
	return f, true
}

func TestGenerateTextOtherInfo_OmitsFrtWhenNoFirstToken(t *testing.T) {
	start := time.Now()
	info := &relaycommon.RelayInfo{
		StartTime: start,
		// Exactly how RelayInfo initialises it: "never happened".
		FirstResponseTime: start.Add(-time.Second),
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}

	if got, present := frtOf(t, info); present {
		t.Errorf("frt = %v, want the key to be absent. A non-streaming request never sets "+
			"FirstResponseTime, so subtracting the sentinel reports a negative time-to-first-token "+
			"(-1000ms) for a request that simply had no streaming phase.", got)
	}
}

func TestGenerateTextOtherInfo_ReportsFrtWhenFirstTokenSeen(t *testing.T) {
	start := time.Now()
	info := &relaycommon.RelayInfo{
		StartTime:         start,
		FirstResponseTime: start.Add(250 * time.Millisecond),
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}

	got, present := frtOf(t, info)
	if !present {
		t.Fatal("frt missing for a request that did observe a first token — the guard must not " +
			"suppress real measurements")
	}
	if got != 250 {
		t.Errorf("frt = %v, want 250", got)
	}
}

// TestGenerateTextOtherInfo_FrtNeverNegative is the invariant the two cases
// above exist to protect, stated directly: whatever the timings, a recorded
// time-to-first-token is never negative.
func TestGenerateTextOtherInfo_FrtNeverNegative(t *testing.T) {
	start := time.Now()
	for _, delta := range []time.Duration{-time.Second, 0, time.Millisecond, time.Second} {
		info := &relaycommon.RelayInfo{StartTime: start, FirstResponseTime: start.Add(delta), ChannelMeta: &relaycommon.ChannelMeta{}}
		if got, present := frtOf(t, info); present && got < 0 {
			t.Errorf("delta=%v recorded frt=%v; a negative time to first token is not a measurement", delta, got)
		}
	}
}
