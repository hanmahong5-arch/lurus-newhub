package openai

import (
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/privateendpoint"
)

// privateEndpointInfo builds the minimum RelayInfo the dispatch path needs.
func privateEndpointInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:      4242,
			ChannelType:    constant.ChannelTypePrivateEndpoint,
			ChannelBaseUrl: baseURL,
		},
	}
}

// TestPrivateEndpointDispatchesToIntranetTarget proves the routing half: a
// private-endpoint channel sends the standard OpenAI-compatible path straight
// at the customer's own host, with no rewriting and no vendor base URL.
func TestPrivateEndpointDispatchesToIntranetTarget(t *testing.T) {
	adaptor := &Adaptor{}
	for _, base := range []string{
		"http://127.0.0.1:11434",
		"http://vllm.lurus-inference.svc.cluster.local:8000",
		"http://10.10.0.7:8000",
	} {
		got, err := adaptor.GetRequestURL(privateEndpointInfo(base))
		if err != nil {
			t.Fatalf("intranet base %s must dispatch, got error: %v", base, err)
		}
		want := base + "/v1/chat/completions"
		if got != want {
			t.Fatalf("base %s: got %q, want %q", base, got, want)
		}
	}
}

// TestPrivateEndpointRefusesPublicEgressAtDispatch is the load-bearing test of
// the whole feature. A channel row whose base_url points at a public provider
// — however it got there, including a direct DB write that never passed the
// HTTP validation handler — must produce NO outbound request at all.
func TestPrivateEndpointRefusesPublicEgressAtDispatch(t *testing.T) {
	adaptor := &Adaptor{}
	for _, base := range []string{
		"https://api.openai.com",
		"https://api.anthropic.com",
		"https://api.deepseek.com",
		"http://8.8.8.8:11434",
		"", // channel created with no base_url at all
	} {
		got, err := adaptor.GetRequestURL(privateEndpointInfo(base))
		if err == nil {
			t.Fatalf("public base %q MUST be refused, but dispatch produced URL %q", base, got)
		}
		if got != "" {
			t.Fatalf("a refused dispatch must return an empty URL, got %q", got)
		}
		// The operator has to be able to find the offending row.
		if !strings.Contains(err.Error(), "4242") {
			t.Fatalf("error must name the channel id, got: %v", err)
		}
		if !strings.Contains(err.Error(), "no request was sent") {
			t.Fatalf("error must state that nothing was emitted, got: %v", err)
		}
	}
}

// TestPrivateEndpointAllowListReachesDispatch proves the escape hatch is wired
// through to the dispatch path too (not only to config validation), so a
// corporate-DNS deployment is usable rather than permanently blocked.
func TestPrivateEndpointAllowListReachesDispatch(t *testing.T) {
	const corporate = "https://inference.datacenter.example.com"
	adaptor := &Adaptor{}

	if _, err := adaptor.GetRequestURL(privateEndpointInfo(corporate)); err == nil {
		t.Fatal("corporate DNS name must be refused by default")
	}

	t.Setenv(privateendpoint.ExtraHostsEnv, "inference.datacenter.example.com")
	got, err := adaptor.GetRequestURL(privateEndpointInfo(corporate))
	if err != nil {
		t.Fatalf("allow-listed host must dispatch: %v", err)
	}
	if got != corporate+"/v1/chat/completions" {
		t.Fatalf("unexpected URL: %q", got)
	}
}

// TestPrivateEndpointIsAFirstClassChannelType pins the registration wiring: the
// type resolves to the OpenAI API type WITH ok=true (an implicit fallback would
// return false and get skipped by handler/model.go's catalogue loop), and it
// carries no default base URL that could point off-premise.
func TestPrivateEndpointIsAFirstClassChannelType(t *testing.T) {
	apiType, ok := common.ChannelType2APIType(constant.ChannelTypePrivateEndpoint)
	if !ok {
		t.Fatal("private endpoint must be an EXPLICITLY mapped channel type, not an implicit fallback")
	}
	if apiType != constant.APITypeOpenAI {
		t.Fatalf("private endpoint must reuse the OpenAI-compatible adaptor, got api type %d", apiType)
	}

	if name := constant.GetChannelTypeName(constant.ChannelTypePrivateEndpoint); name != "PrivateEndpoint" {
		t.Fatalf("channel type name must be registered for console/audit display, got %q", name)
	}

	// The catalogue loop in handler/model.go iterates 1..ChannelTypeDummy, so
	// the sentinel must cover the new type or it silently drops out.
	if constant.ChannelTypeDummy < constant.ChannelTypePrivateEndpoint {
		t.Fatalf("ChannelTypeDummy (%d) must be >= ChannelTypePrivateEndpoint (%d) or the type is excluded from the model catalogue",
			constant.ChannelTypeDummy, constant.ChannelTypePrivateEndpoint)
	}

	if len(constant.ChannelBaseURLs) <= constant.ChannelTypePrivateEndpoint {
		t.Fatalf("ChannelBaseURLs must have an entry at index %d", constant.ChannelTypePrivateEndpoint)
	}
	if def := constant.ChannelBaseURLs[constant.ChannelTypePrivateEndpoint]; def != "" {
		t.Fatalf("a private endpoint must have NO default base URL (a default could point off-premise), got %q", def)
	}
}
