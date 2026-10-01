package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func failoverCtx(t *testing.T) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestFailoverIncompleteStream(t *testing.T) {
	t.Run("nothing written: retryable 502, not skip-retry", func(t *testing.T) {
		err := FailoverIncompleteStream(failoverCtx(t), &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndUpstreamClosed})
		if err == nil {
			t.Fatal("want an error")
		}
		if types.IsSkipRetryError(err) || types.IsChannelError(err) || !types.IsUpstreamFailure(err) || err.StatusCode != http.StatusBadGateway {
			t.Errorf("want retryable 502 upstream failure, got skip=%v channel=%v upstream=%v status=%d",
				types.IsSkipRetryError(err), types.IsChannelError(err), types.IsUpstreamFailure(err), err.StatusCode)
		}
		if err.GetErrorCode() != types.ErrorCodeUpstreamStreamIncomplete {
			t.Errorf("code = %q", err.GetErrorCode())
		}
	})

	t.Run("idle timeout stays 502 (504 is never retried) but names the timeout", func(t *testing.T) {
		err := FailoverIncompleteStream(failoverCtx(t), &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndTimeout})
		if err == nil || err.StatusCode != http.StatusBadGateway || types.IsSkipRetryError(err) {
			t.Fatalf("got %+v", err)
		}
	})

	t.Run("nil info is tolerated", func(t *testing.T) {
		if FailoverIncompleteStream(failoverCtx(t), nil) == nil {
			t.Error("want an error")
		}
	})

	t.Run("bytes already written: nil, in-band path applies", func(t *testing.T) {
		c := failoverCtx(t)
		_, _ = c.Writer.WriteString("data: x\n\n")
		if err := FailoverIncompleteStream(c, &relaycommon.RelayInfo{}); err != nil {
			t.Errorf("a retry would append a second response; got %v", err)
		}
	})

	t.Run("caller gone: nil", func(t *testing.T) {
		c := failoverCtx(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = c.Request.WithContext(ctx)
		if err := FailoverIncompleteStream(c, &relaycommon.RelayInfo{}); err != nil {
			t.Errorf("nobody to fail over for; got %v", err)
		}
	})
}
