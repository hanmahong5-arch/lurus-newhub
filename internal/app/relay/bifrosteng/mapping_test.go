package bifrosteng

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestIsMappedOpenAIFamily(t *testing.T) {
	for _, apiType := range []int{
		constant.APITypeOpenAI,
		constant.APITypeAnthropic,
		constant.APITypeGemini,
		constant.APITypeDeepSeek,
		constant.APITypeOpenRouter,
	} {
		if !IsMapped(apiType) {
			t.Fatalf("apiType %d should be mapped", apiType)
		}
		if CoverageOf(apiType) != CoverageOwned {
			t.Fatalf("apiType %d coverage=%s", apiType, CoverageOf(apiType))
		}
	}
}

func TestUnmappedFallsBack(t *testing.T) {
	if IsMapped(constant.APITypeBaidu) {
		t.Fatal("baidu must not be mapped yet")
	}
	if CoverageOf(constant.APITypeBaidu) != CoverageDeferred {
		t.Fatalf("baidu coverage=%s", CoverageOf(constant.APITypeBaidu))
	}
	for _, apiType := range []int{constant.APITypeAws, constant.APITypeVertexAi} {
		if !IsMapped(apiType) || CoverageOf(apiType) != CoverageOwned {
			t.Fatalf("apiType %d should be bifrost-owned (account extras modeled)", apiType)
		}
	}
}

func TestProviderKeyStable(t *testing.T) {
	if ProviderKey(42) != "nh-ch-42" {
		t.Fatalf("got %s", ProviderKey(42))
	}
}
