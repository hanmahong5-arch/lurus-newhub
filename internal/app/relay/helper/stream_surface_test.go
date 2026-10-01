package helper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// SurfaceIncompleteStream writes the wire's in-band error frame exactly once
// and returns the same failure, marked so the relay's terminal error renderer
// (which must not write a second frame) can recognise it without reading text.
func TestSurfaceIncompleteStream_WritesOneFrameAndMarksTheError(t *testing.T) {
	cases := []struct {
		format types.RelayFormat
		frame  string
	}{
		{types.RelayFormatOpenAI, `data: {"error":{`},
		{types.RelayFormatClaude, "event: error\n"},
		{types.RelayFormatGemini, `data: {"error":{`},
	}
	for _, tc := range cases {
		t.Run(string(tc.format), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndUpstreamClosed}

			apiErr := SurfaceIncompleteStream(c, tc.format, info)
			if apiErr == nil {
				t.Fatal("want the failure back")
			}
			if !IsIncompleteStreamSurfaced(apiErr) {
				t.Error("returned error must be marked as already rendered")
			}
			if !types.IsSkipRetryError(apiErr) || !types.IsUpstreamFailure(apiErr) || apiErr.StatusCode != http.StatusBadGateway {
				t.Errorf("want skip-retry upstream 502, got skip=%v upstream=%v status=%d",
					types.IsSkipRetryError(apiErr), types.IsUpstreamFailure(apiErr), apiErr.StatusCode)
			}
			if apiErr.GetErrorCode() != types.ErrorCodeUpstreamStreamIncomplete {
				t.Errorf("code = %q", apiErr.GetErrorCode())
			}
			if got := strings.Count(rec.Body.String(), tc.frame); got != 1 {
				t.Errorf("error frames written = %d, want 1:\n%s", got, rec.Body.String())
			}
		})
	}
}

// The marker is a type on the cause, not the code or the text: the same
// upstream_stream_incomplete code is also carried by errors nothing has
// rendered yet, and those must still reach the renderer.
func TestIsIncompleteStreamSurfaced_OnlyTheRenderedError(t *testing.T) {
	info := &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndUpstreamClosed}
	if IsIncompleteStreamSurfaced(nil) {
		t.Error("nil is not surfaced")
	}
	if IsIncompleteStreamSurfaced(IncompleteStreamError(info)) {
		t.Error("IncompleteStreamError has not been rendered anywhere: not surfaced")
	}
	if IsIncompleteStreamSurfaced(FailoverIncompleteStream(failoverCtx(t), info)) {
		t.Error("the pre-first-byte failover error has not been rendered: not surfaced")
	}
	if IsIncompleteStreamSurfaced(types.NewErrorWithStatusCode(errString("upstream stream ended before completion"), types.ErrorCodeUpstreamStreamIncomplete, http.StatusBadGateway)) {
		t.Error("same code and text but no marker: not surfaced")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
