package helper

// stream_scanner_terminal_test.go — regression cover for two StreamScannerHandler
// defects, both driven through the real handler (not a re-implementation of its
// parsing logic):
//  1. a bare "[DONE]" terminator (no "data: " prefix) was stripped of 5 bytes
//     unconditionally, so it reached dataHandler as the payload "]" and the
//     terminal marker was never set — the caller then treats the upstream usage
//     as untrustworthy and bills the request as zero.
//  2. the data-handler goroutine writes through the gin *Context, but was not
//     counted by the WaitGroup the cleanup path waits on, so the handler could
//     return — handing c back to gin's sync.Pool — while that write was still
//     in flight.

import (
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
)

// collectStream runs StreamScannerHandler over body and returns every frame the
// data handler received plus whether the terminal marker was reported.
func collectStream(t *testing.T, body string) ([]string, bool) {
	t.Helper()

	c, _ := newStreamCtx()
	info := &relaycommon.RelayInfo{}
	resp := respFromString(body)
	defer func() { _ = resp.Body.Close() }()

	var (
		mu        sync.Mutex
		collected []string
	)
	sawDone := false
	StreamScannerHandler(c, resp, info, func(data string) bool {
		mu.Lock()
		collected = append(collected, data)
		mu.Unlock()
		return true
	}, &sawDone)

	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), collected...), sawDone
}

func TestStreamScannerHandler_TerminalMarkerForms(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantCollected []string
		wantDone      bool
	}{
		{
			// The defect: "[DONE]" is 6 bytes, so the length guard passes and the
			// "[DONE]"-prefix guard suppresses the skip, but the unconditional
			// data[5:] then turned it into the payload "]".
			name:          "bare DONE terminator",
			body:          "data: {\"a\":1}\n\n[DONE]\n\n",
			wantCollected: []string{`{"a":1}`},
			wantDone:      true,
		},
		{
			name:          "bare DONE as the only line",
			body:          "[DONE]\n\n",
			wantCollected: nil,
			wantDone:      true,
		},
		{
			name:          "bare DONE stops the stream",
			body:          "[DONE]\n\ndata: {\"late\":1}\n\n",
			wantCollected: nil,
			wantDone:      true,
		},
		{
			name:          "standard data-prefixed DONE",
			body:          "data: {\"a\":1}\n\ndata: [DONE]\n\n",
			wantCollected: []string{`{"a":1}`},
			wantDone:      true,
		},
		{
			name:          "DONE without a space after the colon",
			body:          "data:{\"a\":1}\n\ndata:[DONE]\n\n",
			wantCollected: []string{`{"a":1}`},
			wantDone:      true,
		},
		{
			// Shapes that must keep behaving exactly as before the fix.
			name:          "non-data lines and empty payloads still skipped",
			body:          ": ping\nid: 1\ndata:\nevent: message\ndata:   \ndata: real\n\ndata: [DONE]\n\n",
			wantCollected: []string{"real"},
			wantDone:      true,
		},
		{
			name:          "payload merely containing DONE is still data",
			body:          "data: {\"text\":\"[DONE]\"}\n\n",
			wantCollected: []string{`{"text":"[DONE]"}`},
			wantDone:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collected, sawDone := collectStream(t, tt.body)

			for _, frame := range collected {
				if frame == "]" {
					t.Errorf("data handler received the mangled frame %q — the terminator was stripped as if it were a payload", frame)
				}
			}
			if sawDone != tt.wantDone {
				t.Errorf("sawTerminalMarker = %v, want %v (a missed marker makes the caller drop real usage and bill zero)", sawDone, tt.wantDone)
			}
			if len(collected) != len(tt.wantCollected) {
				t.Fatalf("collected = %q, want %q", collected, tt.wantCollected)
			}
			for i := range collected {
				if collected[i] != tt.wantCollected[i] {
					t.Errorf("frame %d = %q, want %q", i, collected[i], tt.wantCollected[i])
				}
			}
		})
	}
}

// TestStreamScannerHandler_WaitsForInFlightDataHandler pins defect (2): the
// per-frame writer goroutine must be joined before StreamScannerHandler returns,
// because gin recycles the *gin.Context into its sync.Pool the moment the
// request handler returns and a late write would land on the next request's
// socket.
func TestStreamScannerHandler_WaitsForInFlightDataHandler(t *testing.T) {
	cfg := config.Get()
	prevWrite := cfg.Relay.WriteTimeout
	cfg.Relay.WriteTimeout = 5 * time.Millisecond
	t.Cleanup(func() { cfg.Relay.WriteTimeout = prevWrite })

	c, _ := newStreamCtx()
	info := &relaycommon.RelayInfo{}
	resp := respFromString("data: {\"a\":1}\n\n")
	defer func() { _ = resp.Body.Close() }()

	var (
		entered  = make(chan struct{})
		release  = make(chan struct{})
		returned = make(chan struct{})
	)

	go func() {
		defer close(returned)
		StreamScannerHandler(c, resp, info, func(string) bool {
			close(entered)
			<-release // still holding/writing through c
			return true
		})
	}()

	<-entered
	// The scan loop abandons the frame after WriteTimeout (5ms); give the
	// pre-fix code far more than that to reach its return statement.
	time.Sleep(200 * time.Millisecond)

	select {
	case <-returned:
		close(release)
		t.Fatal("StreamScannerHandler returned while its data-handler goroutine was still writing through the gin Context")
	default:
	}

	close(release)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamScannerHandler never returned after the data handler finished")
	}
}
