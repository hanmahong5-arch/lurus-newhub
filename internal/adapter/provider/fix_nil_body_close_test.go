package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

// fixNilBodyStubUpstream starts a loopback stub that answers every request with
// 200. It is wired in as the channel proxy so doRequest's client.Do succeeds
// without the process ever leaving the machine (the proxy transport carries no
// dial guard and never resolves the target host).
func fixNilBodyStubUpstream(t *testing.T) *common.RelayInfo {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	return &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{Proxy: srv.URL},
		},
	}
}

// fixNilBodyGinContext returns a gin context whose inbound request is usable by
// doRequest.
func fixNilBodyGinContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	return c
}

// TestFixNilBodyClose_UpstreamRequestWithoutBody covers the bodyless upstream
// request shape — http.NewRequestWithContext(ctx, method, url, nil) leaves
// req.Body nil — which callers that build their own *http.Request produce for
// GET-style upstream calls. The close on the success path must tolerate it.
func TestFixNilBodyClose_UpstreamRequestWithoutBody(t *testing.T) {
	info := fixNilBodyStubUpstream(t)
	c := fixNilBodyGinContext(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://fix-nil-body.invalid/v1/models", nil)
	if err != nil {
		t.Fatalf("build upstream request: %v", err)
	}
	if req.Body != nil {
		t.Fatalf("precondition failed: req.Body = %v, want nil", req.Body)
	}

	resp, err := DoRequest(c, req, info)
	if err != nil {
		t.Fatalf("DoRequest with a bodyless upstream request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestFixNilBodyClose_InboundRequestWithoutBody isolates the second close: the
// upstream request carries a body, only the inbound gin request has none.
func TestFixNilBodyClose_InboundRequestWithoutBody(t *testing.T) {
	info := fixNilBodyStubUpstream(t)
	c := fixNilBodyGinContext(t)
	c.Request.Body = nil

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://fix-nil-body.invalid/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatalf("build upstream request: %v", err)
	}
	if req.Body == nil {
		t.Fatal("precondition failed: req.Body must be non-nil for this case")
	}

	resp, err := DoRequest(c, req, info)
	if err != nil {
		t.Fatalf("DoRequest with a bodyless inbound request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
