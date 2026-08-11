package replicate

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------------------------------------------------------------------
// DoResponse: this is the billing-critical response-translation seam.
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil response errors", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		_, apiErr := a.DoResponse(c, nil, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for nil response, got nil")
		}
	})

	t.Run("malformed JSON body errors, does not panic", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser("{not json")}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for malformed body, got nil")
		}
		if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
			t.Errorf("ErrorCode = %q, want bad_response_body", apiErr.GetErrorCode())
		}
	})

	t.Run("prediction error message takes priority over detail/code", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		body := `{"error":{"message":"quota exceeded","detail":"ignored detail","code":"E1"}}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for prediction.error, got nil")
		}
		if !strings.Contains(apiErr.Error(), "quota exceeded") {
			t.Errorf("error = %v, want message to take priority", apiErr.Error())
		}
	})

	t.Run("prediction error falls back to detail when message empty", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		body := `{"error":{"detail":"the detail text","code":"E1"}}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil || !strings.Contains(apiErr.Error(), "the detail text") {
			t.Errorf("error = %v, want detail-text fallback", apiErr)
		}
	})

	t.Run("prediction error falls back to code when message and detail empty", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		body := `{"error":{"code":"E42"}}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil || !strings.Contains(apiErr.Error(), "E42") {
			t.Errorf("error = %v, want code fallback E42", apiErr)
		}
	})

	t.Run("status not succeeded (e.g. failed/canceled) errors rather than treating as success", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		body := `{"status":"failed","output":"https://x/out.png"}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for non-succeeded status, got nil")
		}
	})

	t.Run("empty output array yields an error, not an empty-but-successful response", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		body := `{"status":"succeeded","output":[]}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for empty output, got nil")
		}
	})

	t.Run("string output success path writes url data and 200", func(t *testing.T) {
		c, w := prov_ali_repl_vertex_newGinContext(t)
		body := `{"status":"succeeded","output":"https://replicate.delivery/out1.png"}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		usage, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		if usage == nil {
			t.Fatal("expected non-nil usage on success")
		}
		if w.Code != http.StatusOK {
			t.Errorf("recorder status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "https://replicate.delivery/out1.png") {
			t.Errorf("response body = %q, want it to contain the output URL", w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})

	t.Run("array output collects only string entries", func(t *testing.T) {
		c, w := prov_ali_repl_vertex_newGinContext(t)
		body := `{"status":"succeeded","output":["https://x/1.png", 42, "https://x/2.png", null]}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		respBody := w.Body.String()
		if !strings.Contains(respBody, "https://x/1.png") || !strings.Contains(respBody, "https://x/2.png") {
			t.Errorf("response body = %q, want both string URLs present", respBody)
		}
	})

	t.Run("wantsBase64 with an unreachable image URL propagates the download error", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		prov_ali_repl_vertex_initHTTPClient(t)
		body := `{"status":"succeeded","output":"http://127.0.0.1:1/unreachable.png"}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		info := &relaycommon.RelayInfo{Request: &dto.ImageRequest{ResponseFormat: "b64_json"}}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr == nil {
			t.Fatal("expected error when the base64 download fails, got nil (must not silently succeed with no data)")
		}
	})

	t.Run("wantsBase64 true only for b64_json response format, url passthrough otherwise even with ImageRequest set", func(t *testing.T) {
		c, w := prov_ali_repl_vertex_newGinContext(t)
		body := `{"status":"succeeded","output":"https://replicate.delivery/out2.png"}`
		resp := &http.Response{Body: prov_ali_repl_vertex_nopCloser(body)}
		info := &relaycommon.RelayInfo{Request: &dto.ImageRequest{ResponseFormat: "url"}}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		if !strings.Contains(w.Body.String(), "https://replicate.delivery/out2.png") {
			t.Errorf("response body = %q, want url passthrough (not base64 download attempt)", w.Body.String())
		}
	})
}
