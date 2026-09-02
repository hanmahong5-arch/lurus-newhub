package app

// convert_gemini_usage_parity_test.go — the usageMetadata a Gemini-wire client
// receives must not depend on whether it asked for a stream, and must carry
// every figure the OpenAI-wire upstream reported. Same forcing function as
// convert_usage_parity_test.go, applied to the OpenAI->Gemini direction.

import (
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func fullyPopulatedOpenAIUsageForGemini() dto.Usage {
	return dto.Usage{
		PromptTokens:           3527,
		CompletionTokens:       141,
		TotalTokens:            3668,
		PromptTokensDetails:    dto.InputTokenDetails{CachedTokens: 3456},
		CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 41},
	}
}

func TestGeminiUsageMetadata_StreamAndNonStreamAgree(t *testing.T) {
	u := fullyPopulatedOpenAIUsageForGemini()

	nonStream := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Id:      "resp",
		Model:   "m",
		Choices: []dto.OpenAITextResponseChoice{textChoice("hi", "stop")},
		Usage:   u,
	}, geminiInfo()).UsageMetadata

	stop := "stop"
	usage := u
	chunk := &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{streamChoice("hi", &stop)},
		Usage:   &usage,
	}
	streamed := StreamResponseOpenAI2Gemini(chunk, geminiInfo())
	if streamed == nil {
		t.Fatal("terminal stream chunk converted to nil")
	}
	stream := streamed.UsageMetadata

	if !reflect.DeepEqual(nonStream, stream) {
		t.Errorf("usageMetadata differs by transport.\n non-stream: %+v\n     stream: %+v", nonStream, stream)
	}
}

// Census: every numeric field of GeminiUsageMetadata that has a source in
// dto.Usage must be carried; the rest are listed with the reason.
func TestGeminiUsageMetadata_CarriesEveryFieldItCan(t *testing.T) {
	u := fullyPopulatedOpenAIUsageForGemini()
	got := geminiUsageMetadata(&u)

	carried := map[string]int{
		"PromptTokenCount":        u.PromptTokens,
		"CandidatesTokenCount":    u.CompletionTokens - u.CompletionTokenDetails.ReasoningTokens,
		"ThoughtsTokenCount":      u.CompletionTokenDetails.ReasoningTokens,
		"TotalTokenCount":         u.TotalTokens,
		"CachedContentTokenCount": u.PromptTokensDetails.CachedTokens,
	}
	unsourced := map[string]string{
		"ToolUsePromptTokenCount": "OpenAI-wire usage does not separate tool-use prompt tokens; they are inside prompt_tokens already",
		"PromptTokensDetails":     "per-modality breakdown; OpenAI-wire upstreams reachable here report text only (slice, not numeric)",
	}

	v := reflect.ValueOf(got)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if want, ok := carried[name]; ok {
			if int(v.Field(i).Int()) != want {
				t.Errorf("GeminiUsageMetadata.%s = %d, want %d", name, v.Field(i).Int(), want)
			}
			continue
		}
		if _, ok := unsourced[name]; ok {
			continue
		}
		t.Errorf("GeminiUsageMetadata.%s is neither carried by geminiUsageMetadata nor listed as unsourced — a new wire field nobody maps ships as a silent zero to every Gemini-wire client", name)
	}
}

func TestGeminiUsageMetadata_Boundaries(t *testing.T) {
	if got := geminiUsageMetadata(nil); !reflect.DeepEqual(got, dto.GeminiUsageMetadata{}) {
		t.Errorf("nil usage -> %+v, want zero value", got)
	}
	// An upstream that omits total_tokens must not ship totalTokenCount=0 to a
	// Gemini-wire client that sums on it.
	got := geminiUsageMetadata(&dto.Usage{PromptTokens: 10, CompletionTokens: 5})
	if got.TotalTokenCount != 15 {
		t.Errorf("TotalTokenCount = %d, want 15 (prompt+completion fallback)", got.TotalTokenCount)
	}
}
