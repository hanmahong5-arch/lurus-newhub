package replicate

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// ---------------------------------------------------------------------------
// mapOpenAISizeToFlux / reduceRatio / gcd / normalizeFluxDimension
// ---------------------------------------------------------------------------

func TestMapOpenAISizeToFlux(t *testing.T) {
	tests := []struct {
		name       string
		size       string
		wantOK     bool
		wantAspect string
	}{
		{"square", "512x512", true, "1:1"},
		{"exact 16:9", "1792x1024", true, "16:9"},
		{"exact 9:16", "1024x1792", true, "9:16"},
		{"exact 3:2", "1536x1024", true, "3:2"},
		{"exact 2:3", "1024x1536", true, "2:3"},
		{"reducible to known ratio 4:3", "800x600", true, "4:3"},
		{"reducible to known ratio 4:5", "800x1000", true, "4:5"},
		{"non-standard ratio needs custom dims", "999x333", true, "custom"},
		{"missing x separator", "1024", false, ""},
		{"non-numeric width", "axb", false, ""},
		{"zero width rejected", "0x100", false, ""},
		{"negative height rejected", "100x-100", false, ""},
		{"empty string rejected", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aspect, _, _, ok := mapOpenAISizeToFlux(tt.size)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && aspect != tt.wantAspect {
				t.Errorf("aspect = %q, want %q", aspect, tt.wantAspect)
			}
		})
	}

	t.Run("custom aspect carries normalized width/height within [256,1440] on a 32 step", func(t *testing.T) {
		aspect, width, height, ok := mapOpenAISizeToFlux("999x333")
		if !ok || aspect != "custom" {
			t.Fatalf("aspect=%q ok=%v, want custom/true", aspect, ok)
		}
		if width < 256 || width > 1440 || width%32 != 0 {
			t.Errorf("width = %d, want in [256,1440] and multiple of 32", width)
		}
		if height < 256 || height > 1440 || height%32 != 0 {
			t.Errorf("height = %d, want in [256,1440] and multiple of 32", height)
		}
	})
}

func TestReduceRatio(t *testing.T) {
	tests := []struct {
		w, h       int
		wantW      int
		wantH      int
	}{
		{800, 600, 4, 3},
		{1000, 1000, 1, 1},
		{0, 5, 0, 5}, // gcd(0,5) special-cased below
	}
	for _, tt := range tests {
		gotW, gotH := reduceRatio(tt.w, tt.h)
		if tt.w == 0 {
			// gcd(0,h) = h in Euclidean algorithm, so reduceRatio(0,5) = (0,1)
			if gotH != 1 {
				t.Errorf("reduceRatio(0,%d) = (%d,%d), want height reduced to 1", tt.h, gotW, gotH)
			}
			continue
		}
		if gotW != tt.wantW || gotH != tt.wantH {
			t.Errorf("reduceRatio(%d,%d) = (%d,%d), want (%d,%d)", tt.w, tt.h, gotW, gotH, tt.wantW, tt.wantH)
		}
	}
}

func TestGcd(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{8, 12, 4},
		{7, 13, 1},
		{-8, 12, 4}, // negative input normalized to positive result
		{0, 0, 0},
	}
	for _, tt := range tests {
		if got := gcd(tt.a, tt.b); got != tt.want {
			t.Errorf("gcd(%d,%d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNormalizeFluxDimension(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"below min clamps up to 256", 100, 256},
		{"above max clamps down to 1440", 2000, 1440},
		{"exact step multiple untouched", 512, 512},
		{"remainder below half-step rounds down", 520, 512},  // 520%32=8 < 16
		{"remainder at/above half-step rounds up", 528, 544}, // 528%32=16 >= 16
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeFluxDimension(tt.value)
			if got != tt.want {
				t.Errorf("normalizeFluxDimension(%d) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// uploadFileFromForm edge cases not already exercised via ConvertImageRequest
// ---------------------------------------------------------------------------

func TestUploadFileFromForm(t *testing.T) {
	t.Run("nil info errors", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		_, err := uploadFileFromForm(c, nil, "image")
		if err == nil {
			t.Fatal("expected error for nil info, got nil")
		}
	})

	t.Run("request with no multipart Content-Type fails form parsing", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := uploadFileFromForm(c, info, "image")
		if err == nil {
			t.Fatal("expected error parsing a non-multipart request as a multipart form")
		}
	})

	t.Run("multipart form with no files returns empty string, no error", func(t *testing.T) {
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		got, err := uploadFileFromForm(c, info, "image")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got = %q, want empty string when no file uploaded", got)
		}
	})

	t.Run("falls back to any file field when named candidates are absent", func(t *testing.T) {
		prov_ali_repl_vertex_initHTTPClient(t)
		srv := prov_ali_repl_vertex_uploadServer(t, nil, `{"urls":{"get":"https://replicate.delivery/fallback.png"}}`, http.StatusOK)
		defer srv.Close()

		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "reference_photo", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "k"}}
		got, err := uploadFileFromForm(c, info, "image", "image[]", "image_prompt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://replicate.delivery/fallback.png" {
			t.Errorf("got = %q, want fallback field's uploaded URL", got)
		}
	})

	t.Run("upstream non-2xx status is surfaced as an error", func(t *testing.T) {
		prov_ali_repl_vertex_initHTTPClient(t)
		srv := prov_ali_repl_vertex_uploadServer(t, nil, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
		defer srv.Close()

		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "bad-key"}}
		_, err := uploadFileFromForm(c, info, "image")
		if err == nil {
			t.Fatal("expected error for 401 upload response, got nil")
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("error = %v, want it to mention status 401", err)
		}
	})

	t.Run("malformed JSON upload response errors", func(t *testing.T) {
		prov_ali_repl_vertex_initHTTPClient(t)
		srv := prov_ali_repl_vertex_uploadServer(t, nil, `{not json`, http.StatusOK)
		defer srv.Close()

		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "k"}}
		_, err := uploadFileFromForm(c, info, "image")
		if err == nil {
			t.Fatal("expected error for malformed upload response JSON, got nil")
		}
	})

	t.Run("response missing urls.get errors instead of returning a blank URL", func(t *testing.T) {
		prov_ali_repl_vertex_initHTTPClient(t)
		srv := prov_ali_repl_vertex_uploadServer(t, nil, `{"urls":{}}`, http.StatusOK)
		defer srv.Close()

		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "k"}}
		_, err := uploadFileFromForm(c, info, "image")
		if err == nil {
			t.Fatal("expected error for missing urls.get, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName / not-implemented converters
// ---------------------------------------------------------------------------

func TestAdaptor_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != ModelFlux11Pro {
		t.Errorf("GetModelList() = %v, want [%s]", models, ModelFlux11Pro)
	}
	if got := a.GetChannelName(); got != "replicate" {
		t.Errorf("GetChannelName() = %q, want %q", got, "replicate")
	}
}

func TestAdaptor_UnimplementedConverters(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)

	if _, err := a.ConvertOpenAIRequest(c, nil, nil); err == nil {
		t.Error("ConvertOpenAIRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); err == nil {
		t.Error("ConvertRerankRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertEmbeddingRequest(c, nil, dto.EmbeddingRequest{}); err == nil {
		t.Error("ConvertEmbeddingRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertAudioRequest(c, nil, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, nil, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertClaudeRequest(c, nil, nil); err == nil {
		t.Error("ConvertClaudeRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertGeminiRequest(c, nil, nil); err == nil {
		t.Error("ConvertGeminiRequest: expected not-implemented error, got nil")
	}
}
