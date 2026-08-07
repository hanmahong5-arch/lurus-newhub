package ali

import (
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// ---------------------------------------------------------------------------
// ConvertRerankRequest: return_documents defaults to true when the caller
// omits it (upstream requires an explicit value); TopN must be forwarded by
// pointer without corruption.
// ---------------------------------------------------------------------------

func TestConvertRerankRequest(t *testing.T) {
	t.Run("nil ReturnDocuments defaults to true", func(t *testing.T) {
		req := dto.RerankRequest{Model: "gte-rerank-v2", Query: "q", Documents: []any{"a", "b"}, TopN: 2}
		got := ConvertRerankRequest(req)
		if got.Parameters.ReturnDocuments == nil || !*got.Parameters.ReturnDocuments {
			t.Errorf("ReturnDocuments = %v, want default true", got.Parameters.ReturnDocuments)
		}
		if got.Parameters.TopN == nil || *got.Parameters.TopN != 2 {
			t.Errorf("TopN = %v, want 2", got.Parameters.TopN)
		}
		if got.Input.Query != "q" || len(got.Input.Documents) != 2 {
			t.Errorf("Input = %+v, want query+documents preserved", got.Input)
		}
	})

	t.Run("explicit false ReturnDocuments is respected, not overwritten", func(t *testing.T) {
		f := false
		req := dto.RerankRequest{Model: "gte-rerank-v2", Query: "q", ReturnDocuments: &f}
		got := ConvertRerankRequest(req)
		if got.Parameters.ReturnDocuments == nil || *got.Parameters.ReturnDocuments {
			t.Errorf("ReturnDocuments = %v, want explicit false preserved", got.Parameters.ReturnDocuments)
		}
	})
}

// ---------------------------------------------------------------------------
// RerankHandler: upstream response -> internal Usage/Results translation.
// Usage extraction here is the billing seam for rerank calls.
// ---------------------------------------------------------------------------

func TestRerankHandler(t *testing.T) {
	t.Run("success maps total_tokens into both prompt and total usage", func(t *testing.T) {
		c, w := prov_ali_repl_vertex_newGinContext(t)
		body := `{"output":{"results":[{"index":0,"relevance_score":0.87}]},"usage":{"total_tokens":42},"request_id":"req-1"}`
		resp := &http.Response{StatusCode: 200, Body: prov_ali_repl_vertex_nopCloser(body), Header: http.Header{}}

		apiErr, usage := RerankHandler(c, resp, &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		if usage.PromptTokens != 42 || usage.TotalTokens != 42 {
			t.Errorf("usage = %+v, want prompt=total=42 tokens (ali rerank has no distinct completion token count)", usage)
		}
		if usage.CompletionTokens != 0 {
			t.Errorf("CompletionTokens = %d, want 0 for rerank", usage.CompletionTokens)
		}
		if w.Code != 200 {
			t.Errorf("recorder status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "0.87") {
			t.Errorf("response body = %q, want relevance score forwarded", w.Body.String())
		}
	})

	t.Run("upstream error code surfaced as NewAPIError, not swallowed", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		body := `{"code":"InvalidParameter","message":"bad model","request_id":"req-2"}`
		resp := &http.Response{StatusCode: 400, Body: prov_ali_repl_vertex_nopCloser(body), Header: http.Header{}}

		apiErr, usage := RerankHandler(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for upstream error code, got nil")
		}
		if usage != nil {
			t.Errorf("usage should be nil on error path, got %+v", usage)
		}
		if !strings.Contains(apiErr.Error(), "bad model") {
			t.Errorf("error = %v, want upstream message surfaced", apiErr.Error())
		}
	})

	t.Run("malformed JSON body errors instead of panicking", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		resp := &http.Response{StatusCode: 200, Body: prov_ali_repl_vertex_nopCloser("{not json"), Header: http.Header{}}
		apiErr, usage := RerankHandler(c, resp, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for malformed body, got nil")
		}
		if usage != nil {
			t.Errorf("usage should be nil, got %+v", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// oaiFormEdit2WanxImageEdit: wan-specific image edit request builder.
// ---------------------------------------------------------------------------

func TestOaiFormEdit2WanxImageEdit(t *testing.T) {
	c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "swap colors"}, "image", prov_ali_repl_vertex_pngBytes())
	info := &relaycommon.RelayInfo{}
	req := dto.ImageRequest{Model: "wanx-edit", Prompt: "swap colors", N: 3}

	got, err := oaiFormEdit2WanxImageEdit(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wanInput, ok := got.Input.(WanImageInput)
	if !ok {
		t.Fatalf("Input type = %T, want WanImageInput", got.Input)
	}
	if wanInput.Prompt != "swap colors" {
		t.Errorf("Prompt = %q, want %q", wanInput.Prompt, "swap colors")
	}
	if len(wanInput.Images) != 1 {
		t.Fatalf("Images len = %d, want 1", len(wanInput.Images))
	}
	if got.Parameters.N != 3 {
		t.Errorf("Parameters.N = %d, want 3", got.Parameters.N)
	}
	if ratio := info.PriceData.OtherRatios["n"]; ratio != 3 {
		t.Errorf("PriceData OtherRatios[n] = %v, want 3", ratio)
	}
}

func TestOaiFormEdit2WanxImageEdit_MissingImage(t *testing.T) {
	c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "", nil)
	info := &relaycommon.RelayInfo{}
	_, err := oaiFormEdit2WanxImageEdit(c, info, dto.ImageRequest{Model: "wanx-edit", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error when no image uploaded for wan edit, got nil")
	}
	if !strings.Contains(err.Error(), "get image base64s from form failed") {
		t.Errorf("error = %v, want wrapped 'get image base64s from form failed'", err)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Ali (text.go): duplicate coverage of TopP clamping via the
// package-private helper directly, independent of the Adaptor wiring test.
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Ali_TopPClamp(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"exactly 1 clamped down", 1, 0.999},
		{"exactly 0 clamped up", 0, 0.001},
		{"mid-range untouched", 0.42, 0.42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := requestOpenAI2Ali(dto.GeneralOpenAIRequest{TopP: tt.in})
			if got.TopP != tt.want {
				t.Errorf("TopP = %v, want %v", got.TopP, tt.want)
			}
		})
	}
}
