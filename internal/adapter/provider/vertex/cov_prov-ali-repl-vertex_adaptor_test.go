package vertex

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
)

// prov_ali_repl_vertex_credsJSON returns a minimal valid Credentials JSON blob,
// as would be stored in info.ApiKey for JSON-keyfile channels.
func prov_ali_repl_vertex_credsJSON(projectID string) string {
	return `{"project_id":"` + projectID + `","client_email":"sa@example.iam.gserviceaccount.com","private_key":"","private_key_id":"k1","client_id":"c1"}`
}

// ---------------------------------------------------------------------------
// Init: classifies the request mode from the upstream model name. Getting
// this wrong routes traffic through the wrong protocol adapter entirely.
// ---------------------------------------------------------------------------

func TestAdaptor_Init(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"claude-3-opus-20240229", RequestModeClaude},
		{"claude-sonnet-4-5-20250929", RequestModeClaude},
		{"meta/llama3-405b-instruct-maas", RequestModeLlama},
		{"some-llama-model", RequestModeLlama},
		{"gemini-1.5-pro", RequestModeGemini},
		{"imagen-3.0-generate-001", RequestModeGemini},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			a := &Adaptor{}
			a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tt.model}})
			if a.RequestMode != tt.want {
				t.Errorf("RequestMode for %q = %d, want %d", tt.model, a.RequestMode, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getRequestUrl: JSON-credential vs API-key channel URL construction across
// gemini/claude/llama modes and global vs regional endpoints.
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestUrl_JSONCredentials(t *testing.T) {
	t.Run("malformed credentials JSON in ApiKey errors, not silently mis-authenticates", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "not-json", ApiVersion: "us-central1"},
		}
		_, err := a.getRequestUrl(info, "gemini-1.5-pro", "generateContent")
		if err == nil {
			t.Fatal("expected error for malformed credentials JSON, got nil")
		}
	})

	t.Run("gemini mode global region omits region segments", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: prov_ali_repl_vertex_credsJSON("proj-1"), ApiVersion: "global"},
		}
		got, err := a.getRequestUrl(info, "gemini-1.5-pro", "generateContent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://aiplatform.googleapis.com/v1/projects/proj-1/locations/global/publishers/google/models/gemini-1.5-pro:generateContent"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
		if a.AccountCredentials.ProjectID != "proj-1" {
			t.Errorf("AccountCredentials.ProjectID = %q, want proj-1 (decoded from ApiKey)", a.AccountCredentials.ProjectID)
		}
	})

	t.Run("gemini mode regional includes region in host and path", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: prov_ali_repl_vertex_credsJSON("proj-1"), ApiVersion: "us-central1"},
		}
		got, err := a.getRequestUrl(info, "gemini-1.5-pro", "generateContent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj-1/locations/us-central1/publishers/google/models/gemini-1.5-pro:generateContent"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	})

	t.Run("claude mode global region uses anthropic publisher path", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeClaude}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: prov_ali_repl_vertex_credsJSON("proj-2"), ApiVersion: "global"},
		}
		got, err := a.getRequestUrl(info, "claude-3-opus@20240229", "rawPredict")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://aiplatform.googleapis.com/v1/projects/proj-2/locations/global/publishers/anthropic/models/claude-3-opus@20240229:rawPredict"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	})

	t.Run("claude mode regional includes region", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeClaude}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: prov_ali_repl_vertex_credsJSON("proj-2"), ApiVersion: "europe-west1"},
		}
		got, err := a.getRequestUrl(info, "claude-3-opus@20240229", "streamRawPredict?alt=sse")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://europe-west1-aiplatform.googleapis.com/v1/projects/proj-2/locations/europe-west1/publishers/anthropic/models/claude-3-opus@20240229:streamRawPredict?alt=sse"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	})

	t.Run("llama mode uses the openapi chat completions endpoint", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeLlama}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: prov_ali_repl_vertex_credsJSON("proj-3"), ApiVersion: "us-east1"},
		}
		got, err := a.getRequestUrl(info, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://us-east1-aiplatform.googleapis.com/v1beta1/projects/proj-3/locations/us-east1/endpoints/openapi/chat/completions"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	})

	t.Run("unsupported request mode (zero value) errors", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: prov_ali_repl_vertex_credsJSON("proj-1"), ApiVersion: "global"},
		}
		_, err := a.getRequestUrl(info, "m", "s")
		if err == nil {
			t.Fatal("expected error for unsupported request mode, got nil")
		}
	})
}

func TestAdaptor_GetRequestUrl_APIKeyCredentials(t *testing.T) {
	t.Run("global region, non-SSE suffix uses ? as key separator", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:               "AIza-secret",
				ApiVersion:           "global",
				ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey},
			},
		}
		got, err := a.getRequestUrl(info, "gemini-1.5-pro", "generateContent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-1.5-pro:generateContent?key=AIza-secret"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	})

	t.Run("SSE suffix uses & as key separator (query string already has alt=sse)", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:               "AIza-secret",
				ApiVersion:           "us-central1",
				ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey},
			},
		}
		got, err := a.getRequestUrl(info, "gemini-1.5-pro", "streamGenerateContent?alt=sse")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://us-central1-aiplatform.googleapis.com/v1/publishers/google/models/gemini-1.5-pro:streamGenerateContent?alt=sse&key=AIza-secret"
		if got != want {
			t.Errorf("url = %q, want %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// GetRequestURL: model-name-suffix stripping (-thinking-N, -thinking,
// -nothinking, effort suffixes) and imagen predict override.
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	prov_ali_repl_vertex_withThinkingAdapter(t, true)

	t.Run("thinking-<budget> suffix stripped before request", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "gemini-2.5-flash-thinking-8000",
			},
		}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "/models/gemini-2.5-flash:generateContent") {
			t.Errorf("url = %q, want -thinking-<budget> suffix stripped from model name", got)
		}
		if info.UpstreamModelName != "gemini-2.5-flash" {
			t.Errorf("UpstreamModelName = %q, want stripped", info.UpstreamModelName)
		}
	})

	t.Run("legacy -thinking suffix stripped", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "gemini-2.5-flash-thinking",
			},
		}
		_, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "gemini-2.5-flash" {
			t.Errorf("UpstreamModelName = %q, want -thinking suffix stripped", info.UpstreamModelName)
		}
	})

	t.Run("-nothinking suffix stripped", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "gemini-2.5-flash-nothinking",
			},
		}
		_, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "gemini-2.5-flash" {
			t.Errorf("UpstreamModelName = %q, want -nothinking suffix stripped", info.UpstreamModelName)
		}
	})

	t.Run("reasoning effort suffix stripped", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "gemini-2.5-flash-high",
			},
		}
		_, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "gemini-2.5-flash" {
			t.Errorf("UpstreamModelName = %q, want -high effort suffix stripped", info.UpstreamModelName)
		}
	})

	t.Run("imagen model forces predict suffix instead of generateContent", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "imagen-3.0-generate-001",
			},
		}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(got, ":predict") {
			t.Errorf("url = %q, want :predict suffix for imagen models", got)
		}
	})

	t.Run("stream mode uses streamGenerateContent SSE suffix", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		info := &relaycommon.RelayInfo{
			IsStream: true,
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "gemini-1.5-pro",
			},
		}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, ":streamGenerateContent?alt=sse") {
			t.Errorf("url = %q, want streamGenerateContent SSE suffix", got)
		}
	})

	t.Run("claude mode maps public model id to the vertex-internal model id", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeClaude}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey: prov_ali_repl_vertex_credsJSON("p"), ApiVersion: "global",
				UpstreamModelName: "claude-3-opus-20240229",
			},
		}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "/models/claude-3-opus@20240229:rawPredict") {
			t.Errorf("url = %q, want the mapped claude-3-opus@20240229 model id", got)
		}
	})

	t.Run("unsupported request mode errors", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.GetRequestURL(info)
		if err == nil {
			t.Fatal("expected error for unsupported request mode, got nil")
		}
	})
}

// prov_ali_repl_vertex_withThinkingAdapter toggles the global gemini
// ThinkingAdapterEnabled setting for the duration of the test and restores it.
func prov_ali_repl_vertex_withThinkingAdapter(t *testing.T, enabled bool) {
	t.Helper()
	s := model_setting.GetGeminiSettings()
	prev := s.ThinkingAdapterEnabled
	s.ThinkingAdapterEnabled = enabled
	t.Cleanup(func() { model_setting.GetGeminiSettings().ThinkingAdapterEnabled = prev })
}

// ---------------------------------------------------------------------------
// SetupRequestHeader: API-key channels must not attempt (network-bound)
// service-account token exchange, and must forward claude-specific headers
// only for claude-family models.
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader_APIKeyMode(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:               "AIza-secret",
			UpstreamModelName:    "gemini-1.5-pro",
			ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey},
		},
	}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error (should not attempt token exchange for api_key channels): %v", err)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want unset for api_key channels (auth is via URL ?key=)", got)
	}
	if got := header.Get("x-goog-user-project"); got != "" {
		t.Errorf("x-goog-user-project = %q, want unset (no AccountCredentials populated in api_key mode)", got)
	}
}

func TestAdaptor_SetupRequestHeader_ClaudeHeaders(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	c.Request.Header.Set("anthropic-beta", "tools-2024-04-04")
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-opus-20240229",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:               "AIza-secret",
			UpstreamModelName:    "claude-3-opus@20240229",
			ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey},
		},
	}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("anthropic-beta"); got != "tools-2024-04-04" {
		t.Errorf("anthropic-beta = %q, want forwarded for claude-family model", got)
	}
}

func TestAdaptor_SetupRequestHeader_XGoogUserProject(t *testing.T) {
	a := &Adaptor{AccountCredentials: Credentials{ProjectID: "proj-9"}}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:               "AIza-secret",
			UpstreamModelName:    "gemini-1.5-pro",
			ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey},
		},
	}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("x-goog-user-project"); got != "proj-9" {
		t.Errorf("x-goog-user-project = %q, want proj-9 when AccountCredentials already carries a project id", got)
	}
}
