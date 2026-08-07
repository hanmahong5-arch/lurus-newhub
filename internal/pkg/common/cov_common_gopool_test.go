package common

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestRelayCtxGo_PanicHandlerSignalsStopChanAndLogs drives the process-wide
// relayGoPool's panic handler (installed once in gopool.go's init()) by
// scheduling a function that panics. The handler must both (a) send true on
// a "stop_chan" pulled from the context, when present, and (b) log the panic
// via SysError so it isn't silently swallowed. A stub panic handler (or one
// that forgot the type assertion) would leave stopChan empty and/or the log
// buffer unchanged.
func TestRelayCtxGo_PanicHandlerSignalsStopChanAndLogs(t *testing.T) {
	origOut, origErr := gin.DefaultWriter, gin.DefaultErrorWriter
	errBuf := &bytes.Buffer{}
	gin.DefaultWriter, gin.DefaultErrorWriter = &bytes.Buffer{}, errBuf
	t.Cleanup(func() { gin.DefaultWriter, gin.DefaultErrorWriter = origOut, origErr })

	stopChan := make(chan bool, 1)
	ctx := context.WithValue(context.Background(), "stop_chan", stopChan) //nolint:staticcheck // matches gopool.go's own string-key contract

	RelayCtxGo(ctx, func() {
		panic("cov: synthetic relay pool panic")
	})

	select {
	case v := <-stopChan:
		if !v {
			t.Error("stop_chan should receive true on panic, got false")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("panic handler did not signal stop_chan within timeout")
	}

	// The SysError call is async relative to the stop_chan send in a
	// goroutine pool, so give the log line a brief moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(errBuf.String(), "panic in gopool.RelayPool") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(errBuf.String(), "panic in gopool.RelayPool") {
		t.Errorf("expected panic to be logged via SysError, got %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "cov: synthetic relay pool panic") {
		t.Errorf("expected panic message in log line, got %q", errBuf.String())
	}
}

// TestRelayCtxGo_WithoutStopChan_DoesNotPanicCaller covers the negative type
// assertion branch: when the context carries no "stop_chan" value, the panic
// handler must skip the send (no nil-channel deadlock/panic) while still
// logging.
func TestRelayCtxGo_WithoutStopChan_DoesNotPanicCaller(t *testing.T) {
	origOut, origErr := gin.DefaultWriter, gin.DefaultErrorWriter
	errBuf := &bytes.Buffer{}
	gin.DefaultWriter, gin.DefaultErrorWriter = &bytes.Buffer{}, errBuf
	t.Cleanup(func() { gin.DefaultWriter, gin.DefaultErrorWriter = origOut, origErr })

	done := make(chan struct{})
	RelayCtxGo(context.Background(), func() {
		defer close(done)
		panic("cov: panic with no stop_chan in context")
	})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled function did not run")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(errBuf.String(), "panic in gopool.RelayPool") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(errBuf.String(), "panic in gopool.RelayPool") {
		t.Errorf("expected panic to be logged even without a stop_chan, got %q", errBuf.String())
	}
}
