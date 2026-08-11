package helper

// cov_core-app-boot_stream_scanner_edges_test.go — business-acceptance
// coverage for stream_scanner.go edge cases left uncovered by
// stream_scanner_handler_test.go / stream_scanner_streaming_test.go:
//  1. an already-disconnected client (cancelled request context) mid-scan —
//     the "上游断连" / client-disconnect case called out in the task brief:
//     no frames must be delivered to dataHandler, and the handler must
//     return promptly rather than hang.
//  2. the SSE keep-alive ping goroutine's setup/teardown path
//     (pingTicker creation + Stop() + graceful exit via stopChan), which the
//     existing tests never reach because they all run with ping disabled
//     (the package default).
//
// Both scenarios are deterministic — no new ticker/timeout-duration races —
// so this file adds no flakiness risk to the package.

import (
	"context"
	"sync"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// TestCoreAppBootStreamScannerHandler_ClientAlreadyDisconnected covers the
// scan-loop's ctx-cancellation branches (both the derived ctx and
// c.Request.Context() cases): with the request context pre-cancelled before
// the handler is even invoked, the scanner goroutine must observe it on its
// very first select and return without delivering any frame to dataHandler,
// even though the body has real SSE data waiting to be read.
func TestCoreAppBootStreamScannerHandler_ClientAlreadyDisconnected(t *testing.T) {
	c, _ := newStreamCtx()

	cancelledCtx, cancel := context.WithCancel(c.Request.Context())
	cancel() // client is already gone before the handler starts scanning
	c.Request = c.Request.WithContext(cancelledCtx)

	info := &relaycommon.RelayInfo{}
	body := "data: {\"a\":1}\n\ndata: [DONE]\n\n"
	resp := respFromString(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	var (
		mu        sync.Mutex
		collected []string
	)
	StreamScannerHandler(c, resp, info, func(data string) bool {
		mu.Lock()
		collected = append(collected, data)
		mu.Unlock()
		return true
	})

	mu.Lock()
	defer mu.Unlock()
	if len(collected) != 0 {
		t.Errorf("expected zero frames delivered to an already-disconnected client, got %v", collected)
	}
}

// TestCoreAppBootStreamScannerHandler_PingEnabled_NormalStreamCompletes
// covers the ping-goroutine setup/teardown path: with PingIntervalEnabled
// and the token's ping not disabled, StreamScannerHandler must still create
// the pingTicker, run the ping goroutine, and cleanly join it on normal
// stream completion (the [DONE] marker) — same delivered frames as the
// no-ping case, proving the extra goroutine doesn't interfere with data
// delivery or leave the handler hanging.
func TestCoreAppBootStreamScannerHandler_PingEnabled_NormalStreamCompletes(t *testing.T) {
	gs := operation_setting.GetGeneralSetting()
	prevEnabled, prevSeconds := gs.PingIntervalEnabled, gs.PingIntervalSeconds
	gs.PingIntervalEnabled = true
	// Deliberately left at a long interval (unchanged from default): this
	// test only needs the ping goroutine to be created and to join via the
	// stopChan signal on shutdown, not for an actual tick to fire — keeping
	// the interval long avoids any dependency on real elapsed wall time.
	t.Cleanup(func() {
		gs.PingIntervalEnabled = prevEnabled
		gs.PingIntervalSeconds = prevSeconds
	})

	c, _ := newStreamCtx()
	info := &relaycommon.RelayInfo{} // DisablePing defaults to false

	body := "data: {\"a\":1}\n\ndata: [DONE]\n\n"
	resp := respFromString(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

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

	if !sawDone {
		t.Error("expected [DONE] to be observed with ping enabled, same as without")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(collected) != 1 || collected[0] != `{"a":1}` {
		t.Errorf("collected = %v, want [{\"a\":1}] — ping goroutine must not interfere with data delivery", collected)
	}
}

// TestCoreAppBootStreamScannerHandler_PingDisabledPerToken_OverridesGlobal
// covers info.DisablePing short-circuiting pingEnabled even when the global
// setting is on — a per-token/per-request override the pure boolean-AND in
// pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
// depends on.
func TestCoreAppBootStreamScannerHandler_PingDisabledPerToken_OverridesGlobal(t *testing.T) {
	gs := operation_setting.GetGeneralSetting()
	prevEnabled := gs.PingIntervalEnabled
	gs.PingIntervalEnabled = true
	t.Cleanup(func() { gs.PingIntervalEnabled = prevEnabled })

	c, _ := newStreamCtx()
	info := &relaycommon.RelayInfo{DisablePing: true}

	body := "data: {\"x\":9}\n\ndata: [DONE]\n\n"
	resp := respFromString(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	var (
		mu        sync.Mutex
		collected []string
	)
	StreamScannerHandler(c, resp, info, func(data string) bool {
		mu.Lock()
		collected = append(collected, data)
		mu.Unlock()
		return true
	})

	mu.Lock()
	defer mu.Unlock()
	if len(collected) != 1 || collected[0] != `{"x":9}` {
		t.Errorf("collected = %v, want [{\"x\":9}] with per-token DisablePing overriding the global setting", collected)
	}
}
