package helper

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// StreamScannerHandler records how the upstream stream stopped on
// info.StreamEndReason so the handlers can tell an answer that finished from
// one the upstream abandoned (openai.HandleIncompleteStream,
// claude.HandleStreamFinalResponse).
func TestStreamScannerHandler_RecordsEndReason(t *testing.T) {
	t.Run("[DONE] seen: empty", func(t *testing.T) {
		c, _ := newStreamCtx()
		info := &relaycommon.RelayInfo{}
		resp := respFromString("data: alpha\n\ndata: [DONE]\n\n")
		defer func() { _ = resp.Body.Close() }()
		StreamScannerHandler(c, resp, info, func(string) bool { return true })
		if info.StreamEndReason != "" {
			t.Errorf("StreamEndReason = %q, want empty after the terminator", info.StreamEndReason)
		}
	})

	t.Run("EOF without terminator: upstream_closed", func(t *testing.T) {
		c, _ := newStreamCtx()
		info := &relaycommon.RelayInfo{}
		resp := respFromString("data: alpha\n\ndata: beta\n\n")
		defer func() { _ = resp.Body.Close() }()
		StreamScannerHandler(c, resp, info, func(string) bool { return true })
		if info.StreamEndReason != relaycommon.StreamEndUpstreamClosed {
			t.Errorf("StreamEndReason = %q, want %q", info.StreamEndReason, relaycommon.StreamEndUpstreamClosed)
		}
	})

	t.Run("caller hung up: client_gone", func(t *testing.T) {
		c, _ := newStreamCtx()
		ctx, cancel := context.WithCancel(context.Background())
		c.Request = c.Request.WithContext(ctx)
		info := &relaycommon.RelayInfo{}
		pr, pw := io.Pipe() // upstream never sends anything
		defer func() { _ = pw.Close() }()
		resp := &http.Response{StatusCode: 200, Body: pr, Header: make(http.Header)}
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		StreamScannerHandler(c, resp, info, func(string) bool { return true })
		if info.StreamEndReason != relaycommon.StreamEndClientGone {
			t.Errorf("StreamEndReason = %q, want %q", info.StreamEndReason, relaycommon.StreamEndClientGone)
		}
	})

	t.Run("no frame within StreamingTimeout: streaming_timeout", func(t *testing.T) {
		prev := constant.StreamingTimeout
		constant.StreamingTimeout = 1
		defer func() { constant.StreamingTimeout = prev }()

		c, _ := newStreamCtx()
		info := &relaycommon.RelayInfo{}
		pr, pw := io.Pipe() // upstream stalls forever
		defer func() { _ = pw.Close() }()
		resp := &http.Response{StatusCode: 200, Body: pr, Header: make(http.Header)}
		StreamScannerHandler(c, resp, info, func(string) bool { return true })
		if info.StreamEndReason != relaycommon.StreamEndTimeout {
			t.Errorf("StreamEndReason = %q, want %q", info.StreamEndReason, relaycommon.StreamEndTimeout)
		}
	})
}
