package common

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// Both System One channel types serve exactly one endpoint. The model names
// are taken from the generic heuristics lists (image-generation and
// responses-only), which would otherwise claim them: a mapped or custom name
// must not re-introduce a chat or image endpoint, because the console would
// then offer these models in chat.
func TestGetEndpointTypesByChannelType_SystemOne(t *testing.T) {
	for _, ct := range []int{constant.ChannelTypeTypeSafe, constant.ChannelTypeSystemOneCompatible} {
		for _, model := range []string{"jev-latest", "laya-english", ImageGenerationModels[0], OpenAIResponseOnlyModels[0]} {
			got := GetEndpointTypesByChannelType(ct, model)
			if len(got) != 1 || got[0] != constant.EndpointTypeSystemOne {
				t.Errorf("channel type %d model %q: got %v, want [systemone]", ct, model, got)
			}
		}
	}
}

func TestGetDefaultEndpointInfo_SystemOne(t *testing.T) {
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeSystemOne)
	if !ok {
		t.Fatal("no default endpoint info for systemone")
	}
	if info.Path != "/v1/systemone" || info.Method != "POST" {
		t.Errorf("info = %+v, want POST /v1/systemone", info)
	}
}
