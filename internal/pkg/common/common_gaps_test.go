package common

import (
	"bytes"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// TestGetEndpointTypesByChannelType_Table walks each explicit channel-type case
// plus the default branch (unknown channel) and the two model-driven default
// sub-branches, asserting the exact endpoint priority list each yields.
func TestGetEndpointTypesByChannelType_Table(t *testing.T) {
	cases := []struct {
		name        string
		channelType int
		model       string
		want        []constant.EndpointType
	}{
		{"jina", constant.ChannelTypeJina, "jina-reranker", []constant.EndpointType{constant.EndpointTypeJinaRerank}},
		{"anthropic", constant.ChannelTypeAnthropic, "claude-3", []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}},
		{"aws", constant.ChannelTypeAws, "claude-3", []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}},
		{"gemini", constant.ChannelTypeGemini, "gemini-1.5", []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}},
		{"vertex", constant.ChannelTypeVertexAi, "gemini-1.5", []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}},
		{"openrouter", constant.ChannelTypeOpenRouter, "anything", []constant.EndpointType{constant.EndpointTypeOpenAI}},
		{"sora", constant.ChannelTypeSora, "sora", []constant.EndpointType{constant.EndpointTypeOpenAIVideo}},
		// default branch, plain chat model
		{"unknown_default", 999999, "some-random-model", []constant.EndpointType{constant.EndpointTypeOpenAI}},
		// default branch, response-only model
		{"unknown_responseonly", 999999, "o3-deep-research", []constant.EndpointType{constant.EndpointTypeOpenAIResponse}},
		// default branch, image model (prepended to first)
		{"unknown_image", 999999, "dall-e-3", []constant.EndpointType{constant.EndpointTypeImageGeneration, constant.EndpointTypeOpenAI}},
		// explicit case + image model (image prepended)
		{"openrouter_image", constant.ChannelTypeOpenRouter, "flux-pro", []constant.EndpointType{constant.EndpointTypeImageGeneration, constant.EndpointTypeOpenAI}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetEndpointTypesByChannelType(tc.channelType, tc.model)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got)=%d %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestGetIdentityServiceURL_EnvOverride proves the identity base URL honours a
// deploy-time IDENTITY_SERVICE_URL override and otherwise falls back to the
// vendor-neutral in-cluster default (owner-gated issuer contract).
func TestGetIdentityServiceURL_EnvOverride(t *testing.T) {
	t.Setenv("IDENTITY_SERVICE_URL", "http://override.example:1234")
	if got := getIdentityServiceURL(); got != "http://override.example:1234" {
		t.Errorf("override not honoured: %q", got)
	}
	t.Setenv("IDENTITY_SERVICE_URL", "")
	if got := getIdentityServiceURL(); got != "http://platform-core.lurus-platform.svc.cluster.local:18104" {
		t.Errorf("default not used when unset: %q", got)
	}
}

// TestGetIdentityPublicURL_EnvOverride covers the external-facing URL override +
// default (used in redirect responses).
func TestGetIdentityPublicURL_EnvOverride(t *testing.T) {
	t.Setenv("IDENTITY_PUBLIC_URL", "https://public.example")
	if got := getIdentityPublicURL(); got != "https://public.example" {
		t.Errorf("override not honoured: %q", got)
	}
	t.Setenv("IDENTITY_PUBLIC_URL", "")
	if got := getIdentityPublicURL(); got != "https://identity.lurus.cn" {
		t.Errorf("default not used when unset: %q", got)
	}
}

// TestIsRunningInContainer_NotInContainer forces every container marker off
// (env vars cleared; the /.dockerenv + /proc/1/cgroup probes are absent on the
// host test machine) and asserts the negative branch returns false.
func TestIsRunningInContainer_NotInContainer(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("DOCKER_CONTAINER", "")
	t.Setenv("container", "")
	if IsRunningInContainer() {
		t.Skip("host reports as a container (/.dockerenv or cgroup present); negative branch not reachable here")
	}

	// With a container env var set, the positive env-var branch returns true.
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if !IsRunningInContainer() {
		t.Error("KUBERNETES_SERVICE_HOST set should report in-container")
	}
}

// TestCheckWriter covers both branches of the SSE writer type-assert: a plain
// io.Writer is wrapped, while a value already implementing stringWriter is
// returned as-is.
func TestCheckWriter(t *testing.T) {
	// Plain writer -> wrapped in stringWrapper (else branch).
	plain := &bytes.Buffer{}
	w := checkWriter(plain)
	if _, ok := w.(stringWrapper); !ok {
		t.Errorf("plain writer should be wrapped, got %T", w)
	}
	if _, err := w.writeString("hello"); err != nil {
		t.Fatalf("writeString error: %v", err)
	}
	if plain.String() != "hello" {
		t.Errorf("wrapped writeString did not reach underlying writer: %q", plain.String())
	}

	// Already a stringWriter -> returned unchanged (ok branch).
	already := stringWrapper{&bytes.Buffer{}}
	if got := checkWriter(already); got != stringWriter(already) {
		t.Errorf("existing stringWriter should be returned as-is, got %T", got)
	}
}
