package helper

import (
	"context"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// The behavioural assertion for this lives in
// internal/adapter/provider/openai: TestOaiStreamHandler_IncompleteStream_
// ClientGoneWritesNothing drives a real handler with a cancelled request
// context. It is the right test but it is not a reliable gate, because it
// exercises the decision through a select whose two relevant cases are both
// ready: locally the wrong branch is taken about 2 times in 900, and it took a
// -race run in CI to surface it at all.
//
// This table drives the decision directly, so every combination is checked on
// every run instead of whenever the scheduler cooperates. It is deliberately
// exhaustive over the two inputs — the defect was a missing combination, not a
// wrong value in a combination somebody had thought about.
func TestStreamEndReasonForStop(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name         string
		ctxErr       error
		terminalSeen bool
		want         string
	}{
		{
			// The regression. The caller hung up and the upstream read ended
			// as a consequence, so both select cases were ready and the
			// recorded reason was whichever one Go picked. Blaming the
			// provider here is not a cosmetic mislabel: it writes an ERR line
			// and feeds the upstream-failure series that alerting reads.
			name:         "caller gone, no terminator",
			ctxErr:       cancelled.Err(),
			terminalSeen: false,
			want:         relaycommon.StreamEndClientGone,
		},
		{
			name:         "upstream ended early, caller still there",
			ctxErr:       nil,
			terminalSeen: false,
			want:         relaycommon.StreamEndUpstreamClosed,
		},
		{
			// A terminator arrived: the stream is complete and there is no
			// incomplete-stream reason to record. Empty, not client_gone —
			// a caller that disconnects after a complete stream got what it
			// asked for, and marking it would put complete streams into the
			// abnormal-end series.
			name:         "terminator seen, caller gone after",
			ctxErr:       cancelled.Err(),
			terminalSeen: true,
			want:         "",
		},
		{
			name:         "terminator seen, caller still there",
			ctxErr:       nil,
			terminalSeen: true,
			want:         "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamEndReasonForStop(tc.ctxErr, tc.terminalSeen); got != tc.want {
				t.Errorf("streamEndReasonForStop(%v, %v) = %q, want %q",
					tc.ctxErr, tc.terminalSeen, got, tc.want)
			}
		})
	}
}

// The three reasons must stay distinct strings. They are emitted as a metric
// label and read by the incomplete-stream handlers on three wires; two of them
// collapsing into one value would silently merge "the provider broke" with
// "the customer left" — the exact confusion this file exists to prevent — and
// every equality check above would still pass.
func TestStreamEndReasonsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for name, value := range map[string]string{
		"StreamEndTimeout":        relaycommon.StreamEndTimeout,
		"StreamEndUpstreamClosed": relaycommon.StreamEndUpstreamClosed,
		"StreamEndClientGone":     relaycommon.StreamEndClientGone,
	} {
		if value == "" {
			t.Errorf("%s is empty, which is the sentinel for 'stream ended normally'", name)
		}
		if prev, dup := seen[value]; dup {
			t.Errorf("%s and %s are both %q", prev, name, value)
		}
		seen[value] = name
	}
}
