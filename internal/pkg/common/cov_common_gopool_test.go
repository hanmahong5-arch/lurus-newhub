package common

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// covGopoolSyncBuffer is a bytes.Buffer that is safe to read while a pool
// goroutine is still writing to it. gin.DefaultErrorWriter is process-wide and
// the panic handler logs from the pool goroutine, so polling a plain
// bytes.Buffer from the test body is a real data race.
type covGopoolSyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *covGopoolSyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *covGopoolSyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// pinTextModeErrorSink points SysError's output at errBuf for the duration of
// t, and puts both globals it touches back afterwards.
//
// SysError has TWO sinks and picks between them on a process-global flag: in
// text mode it Fprintf's to gin.DefaultErrorWriter, in JSON mode it writes a
// slog record to the logger InitSlog built and never touches
// gin.DefaultErrorWriter at all. The flag (IsJSONLogFormat) is whatever the
// last InitSlog call set, and several tests in this package install a
// JSON-format logger without putting it back — r5b_syslog_format_test.go and
// slog_extra_test.go among them. Swapping gin.DefaultErrorWriter alone
// therefore only worked while these tests happened to run first; under
// `go test -shuffle=on` they run after, SysError goes to slog, the buffer
// stays empty and the assertion reads as "the panic was never logged"
// (cycle-12 L1). Establishing the mode is the fix — the test asserts about the
// text sink, so it has to own the mode.
func pinTextModeErrorSink(t *testing.T, errBuf *covGopoolSyncBuffer) {
	t.Helper()
	origOut, origErr := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = &covGopoolSyncBuffer{}, errBuf

	prevJSON := IsJSONLogFormat()
	textCfg := DefaultSlogConfig() // JSONFormat false
	textCfg.Writer, textCfg.ErrWriter = io.Discard, io.Discard
	InitSlog(textCfg)

	t.Cleanup(func() {
		// Put the FORMAT back so this test is not itself the next one's
		// ordering problem. The writers go back to the package defaults rather
		// than to whatever an earlier leak left, which is the deterministic
		// choice: DefaultSlogConfig is what a process that never called
		// InitSlog would use.
		restore := DefaultSlogConfig()
		restore.JSONFormat = prevJSON
		InitSlog(restore)
		gin.DefaultWriter, gin.DefaultErrorWriter = origOut, origErr
	})
}

// TestRelayCtxGo_PanicHandlerSignalsStopChanAndLogs drives the process-wide
// relayGoPool's panic handler (installed once in gopool.go's init()) by
// scheduling a function that panics. The handler must both (a) send true on
// a "stop_chan" pulled from the context, when present, and (b) log the panic
// via SysError so it isn't silently swallowed. A stub panic handler (or one
// that forgot the type assertion) would leave stopChan empty and/or the log
// buffer unchanged.
func TestRelayCtxGo_PanicHandlerSignalsStopChanAndLogs(t *testing.T) {
	errBuf := &covGopoolSyncBuffer{}
	pinTextModeErrorSink(t, errBuf)

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
	errBuf := &covGopoolSyncBuffer{}
	pinTextModeErrorSink(t, errBuf)

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
