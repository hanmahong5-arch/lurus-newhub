package helper

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
)

// NOTE: the keep-alive ping and DebugEnabled-branch tests were removed. They
// provoked data races: the ping test read the httptest response buffer while an
// untracked inner ping-writer goroutine was still writing it, and the debug
// test toggled the process-wide common.DebugEnabled flag (which production sets
// once at startup, never at runtime) while a scanner goroutine read it in its
// deferred exit trace. The first cause is gone — the inner ping/data writers are
// now counted by the same WaitGroup the cleanup path waits on, so they are
// joined before StreamScannerHandler returns (see
// stream_scanner_terminal_test.go). The DebugEnabled race is a test-only
// artifact and those tests stay removed.

// TestStreamScannerHandler_DataHandlerTimeout forces the per-write timeout branch
// by making the data handler block far longer than the configured WriteTimeout.
func TestStreamScannerHandler_DataHandlerTimeout(t *testing.T) {
	relayCfg := config.Get()
	prevWrite := relayCfg.Relay.WriteTimeout
	relayCfg.Relay.WriteTimeout = 5 * time.Millisecond
	t.Cleanup(func() { relayCfg.Relay.WriteTimeout = prevWrite })

	c, _ := newStreamCtx()
	info := &relaycommon.RelayInfo{}

	// handlerEntered is written from the data-handler goroutine (StreamScannerHandler
	// dispatches the handler via common.SafeGo) and read here after the timeout branch
	// returns while that goroutine may still be sleeping — use atomic to stay race-free
	// under the -race gate.
	var handlerEntered atomic.Int32
	body := "data: {\"slow\":1}\n\ndata: {\"never\":2}\n\n"
	resp := respFromString(body)
	defer func() { _ = resp.Body.Close() }()
	StreamScannerHandler(c, resp, info, func(string) bool {
		handlerEntered.Add(1)
		time.Sleep(60 * time.Millisecond) // exceeds WriteTimeout -> timeout branch
		return true
	})

	// The scanner must bail out after the first (timed-out) frame, so the second
	// frame is never delivered.
	if got := handlerEntered.Load(); got != 1 {
		t.Errorf("handler entered %d times, want 1 (timeout should stop the loop)", got)
	}
}

// TestStreamScannerHandler_ScannerTooLongError drives the scanner.Err() != EOF
// branch by feeding a single line larger than the scanner's max buffer.
func TestStreamScannerHandler_ScannerTooLongError(t *testing.T) {
	relayCfg := config.Get()
	prevInit, prevMax := relayCfg.Relay.StreamScannerInitialBuffer, relayCfg.Relay.StreamScannerMaxBuffer
	relayCfg.Relay.StreamScannerInitialBuffer = 64
	relayCfg.Relay.StreamScannerMaxBuffer = 128
	t.Cleanup(func() {
		relayCfg.Relay.StreamScannerInitialBuffer = prevInit
		relayCfg.Relay.StreamScannerMaxBuffer = prevMax
	})

	c, _ := newStreamCtx()
	info := &relaycommon.RelayInfo{}

	// A single line far exceeding 128 bytes triggers bufio.ErrTooLong (not EOF).
	huge := "data: " + strings.Repeat("x", 5000) + "\n\n"
	var delivered int
	resp := respFromString(huge)
	defer func() { _ = resp.Body.Close() }()
	StreamScannerHandler(c, resp, info, func(string) bool {
		delivered++
		return true
	})

	if delivered != 0 {
		t.Errorf("delivered %d frames, want 0 (over-long line should error out)", delivered)
	}
}
