package replicate

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info errors instead of panicking", func(t *testing.T) {
		_, err := a.GetRequestURL(nil)
		if err == nil {
			t.Fatal("expected error for nil info, got nil")
		}
	})

	t.Run("empty ChannelBaseUrl falls back to the built-in replicate base URL", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := constant.ChannelBaseURLs[constant.ChannelTypeReplicate]
		if got != want {
			t.Errorf("GetRequestURL() = %q, want default %q", got, want)
		}
		if info.ChannelBaseUrl != want {
			t.Errorf("info.ChannelBaseUrl should be mutated to the default, got %q", info.ChannelBaseUrl)
		}
	})

	t.Run("empty RequestURLPath returns bare base url", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"}}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://custom.example.com" {
			t.Errorf("GetRequestURL() = %q, want bare base url", got)
		}
	})

	t.Run("RequestURLPath is appended to base url", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			RequestURLPath: "/v1/models/foo/predictions",
		}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://custom.example.com/v1/models/foo/predictions"
		if got != want {
			t.Errorf("GetRequestURL() = %q, want %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info errors", func(t *testing.T) {
		err := a.SetupRequestHeader(nil, &http.Header{}, nil)
		if err == nil {
			t.Fatal("expected error for nil info, got nil")
		}
	})

	t.Run("missing api key errors -- must not send unauthenticated upstream request", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		err := a.SetupRequestHeader(c, &http.Header{}, info)
		if err == nil {
			t.Fatal("expected error for empty api key, got nil")
		}
	})

	t.Run("sets bearer auth, Prefer:wait, and default content-type/accept", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "r8-secret"}}
		header := http.Header{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer r8-secret" {
			t.Errorf("Authorization = %q, want Bearer r8-secret", got)
		}
		if got := header.Get("Prefer"); got != "wait" {
			t.Errorf("Prefer = %q, want wait (sync prediction completion)", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json default", got)
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json default", got)
		}
	})

	t.Run("inbound multipart content-type is forwarded, not overridden by the json default", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "r8-secret"}}
		header := http.Header{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Content-Type"); got != "multipart/form-data; boundary=x" {
			t.Errorf("Content-Type = %q, want inbound multipart value preserved", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info errors", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		_, err := a.ConvertImageRequest(c, nil, dto.ImageRequest{Prompt: "x"})
		if err == nil {
			t.Fatal("expected error for nil info, got nil")
		}
	})

	t.Run("empty prompt with no form fallback errors", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "   "})
		if err == nil {
			t.Fatal("expected error for blank prompt, got nil")
		}
		if !strings.Contains(err.Error(), "prompt is required") {
			t.Errorf("error = %v, want 'prompt is required'", err)
		}
	})

	t.Run("empty struct prompt falls back to form value", func(t *testing.T) {
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "a cat astronaut"}, "", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("result type = %T, want map[string]any", got)
		}
		input, ok := payload["input"].(map[string]any)
		if !ok {
			t.Fatalf("input type = %T, want map[string]any", payload["input"])
		}
		if input["prompt"] != "a cat astronaut" {
			t.Errorf("input[prompt] = %v, want form-fallback prompt", input["prompt"])
		}
	})

	t.Run("model name precedence: UpstreamModelName > request.Model > default", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "black-forest-labs/flux-schnell"}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x", Model: "ignored-model"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "black-forest-labs/flux-schnell" {
			t.Errorf("UpstreamModelName = %q, want unchanged upstream value", info.UpstreamModelName)
		}
		if info.RequestURLPath != "/v1/models/black-forest-labs/flux-schnell/predictions" {
			t.Errorf("RequestURLPath = %q, want built from upstream model name", info.RequestURLPath)
		}
	})

	t.Run("no upstream/request model name defaults to flux-1.1-pro", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != ModelFlux11Pro {
			t.Errorf("UpstreamModelName = %q, want default %q", info.UpstreamModelName, ModelFlux11Pro)
		}
	})

	sizeTests := []struct {
		name       string
		size       string
		wantAspect string
		wantWidth  int
		wantHeight int
	}{
		{"square", "1024x1024", "1:1", 0, 0},
		{"landscape 16:9 exact", "1792x1024", "16:9", 0, 0},
		{"portrait 9:16 exact", "1024x1792", "9:16", 0, 0},
		{"non-standard ratio falls back to custom width/height", "1000x333", "custom", 0, 0},
	}
	for _, tt := range sizeTests {
		t.Run("size mapping/"+tt.name, func(t *testing.T) {
			c, _ := prov_ali_repl_vertex_newGinContext(t)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x", Size: tt.size})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			input := got.(map[string]any)["input"].(map[string]any)
			if input["aspect_ratio"] != tt.wantAspect {
				t.Errorf("aspect_ratio = %v, want %v", input["aspect_ratio"], tt.wantAspect)
			}
			if tt.wantAspect == "custom" {
				if _, ok := input["width"]; !ok {
					t.Errorf("expected width to be set for custom aspect, input=%v", input)
				}
				if _, ok := input["height"]; !ok {
					t.Errorf("expected height to be set for custom aspect, input=%v", input)
				}
			}
		})
	}

	t.Run("invalid size string is silently ignored (no aspect_ratio key)", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x", Size: "not-a-size"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := got.(map[string]any)["input"].(map[string]any)
		if _, ok := input["aspect_ratio"]; ok {
			t.Errorf("expected no aspect_ratio for unparseable size, input=%v", input)
		}
	})

	t.Run("output_format extracted from raw JSON string", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req := dto.ImageRequest{Prompt: "x", OutputFormat: json.RawMessage(`"webp"`)}
		got, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := got.(map[string]any)["input"].(map[string]any)
		if input["output_format"] != "webp" {
			t.Errorf("output_format = %v, want webp", input["output_format"])
		}
	})

	t.Run("N>0 maps to num_outputs", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x", N: 4})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := got.(map[string]any)["input"].(map[string]any)
		if input["num_outputs"] != 4 {
			t.Errorf("num_outputs = %v, want 4", input["num_outputs"])
		}
	})

	t.Run("quality hd/high enables prompt_upsampling", func(t *testing.T) {
		for _, q := range []string{"hd", "HD", "high", "High"} {
			c, _ := prov_ali_repl_vertex_newGinContext(t)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x", Quality: q})
			if err != nil {
				t.Fatalf("unexpected error for quality=%s: %v", q, err)
			}
			input := got.(map[string]any)["input"].(map[string]any)
			if input["prompt_upsampling"] != true {
				t.Errorf("quality=%s: prompt_upsampling = %v, want true", q, input["prompt_upsampling"])
			}
		}
	})

	t.Run("quality standard does not enable prompt_upsampling", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x", Quality: "standard"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := got.(map[string]any)["input"].(map[string]any)
		if _, ok := input["prompt_upsampling"]; ok {
			t.Errorf("expected no prompt_upsampling key, input=%v", input)
		}
	})

	t.Run("extra_fields JSON is merged into input", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req := dto.ImageRequest{Prompt: "x", ExtraFields: json.RawMessage(`{"guidance":7.5,"seed":42}`)}
		got, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := got.(map[string]any)["input"].(map[string]any)
		if input["guidance"] != 7.5 || input["seed"] != float64(42) {
			t.Errorf("input = %v, want extra_fields merged (guidance=7.5, seed=42)", input)
		}
	})

	t.Run("malformed extra_fields JSON errors instead of silently dropping", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req := dto.ImageRequest{Prompt: "x", ExtraFields: json.RawMessage(`{not json`)}
		_, err := a.ConvertImageRequest(c, info, req)
		if err == nil {
			t.Fatal("expected error for malformed extra_fields, got nil")
		}
	})

	t.Run("edits relay mode without an uploaded image errors", func(t *testing.T) {
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "edit"}, "", nil)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "edit"})
		if err == nil {
			t.Fatal("expected error for missing image on edits mode, got nil")
		}
	})

	t.Run("edits relay mode uploads image and sets image_prompt", func(t *testing.T) {
		prov_ali_repl_vertex_initHTTPClient(t)
		called := false
		srv := prov_ali_repl_vertex_uploadServer(t, func() {
			called = true
		}, `{"urls":{"get":"https://replicate.delivery/uploaded.png"}}`, http.StatusOK)
		defer srv.Close()

		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "edit"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeImagesEdits,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "r8-secret"},
		}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "edit"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !called {
			t.Fatal("expected upload server to be hit")
		}
		input := got.(map[string]any)["input"].(map[string]any)
		if input["image_prompt"] != "https://replicate.delivery/uploaded.png" {
			t.Errorf("image_prompt = %v, want uploaded URL", input["image_prompt"])
		}
	})
}
