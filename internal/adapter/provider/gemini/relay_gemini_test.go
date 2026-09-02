package gemini

import (
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// isNew25ProModel
// ---------------------------------------------------------------------------

func TestIsNew25ProModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		// new 25 pro models (match)
		{"gemini-2.5-pro-preview-06-01", true},
		{"gemini-2.5-pro-latest", true},
		{"gemini-2.5-pro-exp-0325", true},
		// excluded old pro preview models
		{"gemini-2.5-pro-preview-05-06", false},
		{"gemini-2.5-pro-preview-03-25", false},
		// not pro models
		{"gemini-2.0-flash", false},
		{"gemini-1.5-pro", false},
		{"gemini-2.5-flash-preview", false},
		{"gemini-3-pro-preview", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := isNew25ProModel(tt.model)
			if got != tt.want {
				t.Errorf("isNew25ProModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// is25FlashLiteModel
// ---------------------------------------------------------------------------

func TestIs25FlashLiteModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-flash-lite-preview", true},
		{"gemini-2.5-flash-lite-001", true},
		{"gemini-2.5-flash-preview", false},
		{"gemini-2.0-flash-lite-preview", false},
		{"gemini-1.5-flash", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := is25FlashLiteModel(tt.model)
			if got != tt.want {
				t.Errorf("is25FlashLiteModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// clampThinkingBudget
// ---------------------------------------------------------------------------

func TestClampThinkingBudget(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		budget int
		want   int
	}{
		// new 25 pro model: range [128, 32768]
		{"pro25 within range", "gemini-2.5-pro-latest", 5000, 5000},
		{"pro25 below min", "gemini-2.5-pro-latest", 50, pro25MinBudget},
		{"pro25 above max", "gemini-2.5-pro-latest", 50000, pro25MaxBudget},
		{"pro25 at min", "gemini-2.5-pro-latest", pro25MinBudget, pro25MinBudget},
		{"pro25 at max", "gemini-2.5-pro-latest", pro25MaxBudget, pro25MaxBudget},

		// flash-lite model: range [512, 24576]
		{"flash-lite within range", "gemini-2.5-flash-lite-preview", 5000, 5000},
		{"flash-lite below min", "gemini-2.5-flash-lite-preview", 100, flash25LiteMinBudget},
		{"flash-lite above max", "gemini-2.5-flash-lite-preview", 30000, flash25LiteMaxBudget},
		{"flash-lite at min", "gemini-2.5-flash-lite-preview", flash25LiteMinBudget, flash25LiteMinBudget},
		{"flash-lite at max", "gemini-2.5-flash-lite-preview", flash25LiteMaxBudget, flash25LiteMaxBudget},

		// other model: range [0, 24576]
		{"other within range", "gemini-2.0-flash", 10000, 10000},
		{"other at zero", "gemini-2.0-flash", 0, 0},
		{"other negative", "gemini-2.0-flash", -1, 0},
		{"other above max", "gemini-2.0-flash", 30000, flash25MaxBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampThinkingBudget(tt.model, tt.budget)
			if got != tt.want {
				t.Errorf("clampThinkingBudget(%q, %d) = %d, want %d", tt.model, tt.budget, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// clampThinkingBudgetByEffort
// ---------------------------------------------------------------------------

func TestClampThinkingBudgetByEffort(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		effort string
		want   int
	}{
		// new 25 pro: maxBudget = 32768
		{"pro25 high", "gemini-2.5-pro-latest", "high", 32768 * 80 / 100},
		{"pro25 medium", "gemini-2.5-pro-latest", "medium", 32768 * 50 / 100},
		{"pro25 low", "gemini-2.5-pro-latest", "low", 32768 * 20 / 100},
		{"pro25 minimal", "gemini-2.5-pro-latest", "minimal", 32768 * 5 / 100},

		// other model (flash): maxBudget = 24576
		{"flash high", "gemini-2.0-flash", "high", 24576 * 80 / 100},
		{"flash medium", "gemini-2.0-flash", "medium", 24576 * 50 / 100},
		{"flash low", "gemini-2.0-flash", "low", 24576 * 20 / 100},
		{"flash minimal", "gemini-2.0-flash", "minimal", 24576 * 5 / 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampThinkingBudgetByEffort(tt.model, tt.effort)
			if got != tt.want {
				t.Errorf("clampThinkingBudgetByEffort(%q, %q) = %d, want %d", tt.model, tt.effort, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertGeminiRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertGeminiRequest(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	t.Run("default role fix for first content", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{
					Role:  "", // empty role
					Parts: []dto.GeminiPart{{Text: "Hello"}},
				},
			},
		}
		result, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		geminiReq := result.(*dto.GeminiChatRequest)
		if geminiReq.Contents[0].Role != "user" {
			t.Errorf("Role = %q, want %q", geminiReq.Contents[0].Role, "user")
		}
	})

	t.Run("preserves non-empty role", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{
					Role:  "model",
					Parts: []dto.GeminiPart{{Text: "Hi"}},
				},
			},
		}
		result, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		geminiReq := result.(*dto.GeminiChatRequest)
		if geminiReq.Contents[0].Role != "model" {
			t.Errorf("Role = %q, want %q", geminiReq.Contents[0].Role, "model")
		}
	})

	t.Run("YouTube video MIME type fix", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{
					Role: "user",
					Parts: []dto.GeminiPart{
						{
							FileData: &dto.GeminiFileData{
								FileUri:  "https://www.youtube.com/watch?v=abc123",
								MimeType: "",
							},
						},
					},
				},
			},
		}
		result, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		geminiReq := result.(*dto.GeminiChatRequest)
		mime := geminiReq.Contents[0].Parts[0].FileData.MimeType
		if mime != "video/webm" {
			t.Errorf("MimeType = %q, want %q", mime, "video/webm")
		}
	})

	t.Run("non-YouTube file data unchanged", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{
					Role: "user",
					Parts: []dto.GeminiPart{
						{
							FileData: &dto.GeminiFileData{
								FileUri:  "https://example.com/file.pdf",
								MimeType: "application/pdf",
							},
						},
					},
				},
			},
		}
		_, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Contents[0].Parts[0].FileData.MimeType != "application/pdf" {
			t.Error("MIME type should not change for non-YouTube URLs")
		}
	})

	t.Run("empty contents no panic", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{},
		}
		_, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertImageRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	t.Run("non-imagen model returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "test"})
		if err == nil {
			t.Fatal("expected error for non-imagen model")
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Errorf("error = %q, want contains 'not supported'", err.Error())
		}
	})

	t.Run("size to aspect ratio mapping", func(t *testing.T) {
		tests := []struct {
			size string
			want string
		}{
			{"1024x1024", "1:1"},
			{"256x256", "1:1"},
			{"512x512", "1:1"},
			{"1536x1024", "3:2"},
			{"1024x1536", "2:3"},
			{"1024x1792", "9:16"},
			{"1792x1024", "16:9"},
			{"", "1:1"},         // default
			{"3:4", "3:4"},      // direct ratio passthrough
			{"16:9", "16:9"},    // direct ratio passthrough
		}
		for _, tt := range tests {
			t.Run("size_"+tt.size, func(t *testing.T) {
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-002"}}
				result, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
					Prompt: "a cat",
					N:      1,
					Size:   tt.size,
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				geminiReq := result.(dto.GeminiImageRequest)
				if geminiReq.Parameters.AspectRatio != tt.want {
					t.Errorf("AspectRatio = %q, want %q", geminiReq.Parameters.AspectRatio, tt.want)
				}
			})
		}
	})

	t.Run("quality to imageSize mapping", func(t *testing.T) {
		tests := []struct {
			quality   string
			wantSize  string
		}{
			{"hd", "2K"},
			{"high", "2K"},
			{"2K", "2K"},
			{"standard", "1K"},
			{"medium", "1K"},
			{"low", "1K"},
			{"auto", "1K"},
			{"1K", "1K"},
			{"unknown", "1K"},
		}
		for _, tt := range tests {
			t.Run("quality_"+tt.quality, func(t *testing.T) {
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-002"}}
				result, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
					Prompt:  "a cat",
					N:       1,
					Quality: tt.quality,
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				geminiReq := result.(dto.GeminiImageRequest)
				if geminiReq.Parameters.ImageSize != tt.wantSize {
					t.Errorf("ImageSize = %q, want %q", geminiReq.Parameters.ImageSize, tt.wantSize)
				}
			})
		}
	})

	t.Run("prompt and sample count preserved", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-002"}}
		result, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
			Prompt: "a beautiful landscape",
			N:      3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		geminiReq := result.(dto.GeminiImageRequest)
		if len(geminiReq.Instances) != 1 {
			t.Fatalf("len(Instances) = %d, want 1", len(geminiReq.Instances))
		}
		if geminiReq.Instances[0].Prompt != "a beautiful landscape" {
			t.Errorf("Prompt = %q, want %q", geminiReq.Instances[0].Prompt, "a beautiful landscape")
		}
		if geminiReq.Parameters.SampleCount != 3 {
			t.Errorf("SampleCount = %d, want 3", geminiReq.Parameters.SampleCount)
		}
		if geminiReq.Parameters.PersonGeneration != "allow_adult" {
			t.Errorf("PersonGeneration = %q, want %q", geminiReq.Parameters.PersonGeneration, "allow_adult")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.GetChannelName / GetModelList
// ---------------------------------------------------------------------------

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != ChannelName {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), ChannelName)
	}
	if a.GetChannelName() != "google gemini" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "google gemini")
	}
}

func TestAdaptor_GetModelList(t *testing.T) {
	a := &Adaptor{}
	list := a.GetModelList()
	if list == nil {
		t.Fatal("GetModelList() should not be nil")
	}
	if len(list) == 0 {
		t.Error("GetModelList() should return non-empty list")
	}
	// verify a known model is in the list
	found := false
	for _, m := range list {
		if m == "gemini-2.0-flash" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected gemini-2.0-flash in model list")
	}
}

// ---------------------------------------------------------------------------
// buildUsageFromGeminiMetadata
// ---------------------------------------------------------------------------

func TestBuildUsageFromGeminiMetadata(t *testing.T) {
	tests := []struct {
		name              string
		metadata          dto.GeminiUsageMetadata
		wantPrompt        int
		wantCompletion    int
		wantTotal         int
		wantReasoning     int
		wantAudioTokens   int
		wantTextTokens    int
	}{
		{
			name: "basic all fields",
			metadata: dto.GeminiUsageMetadata{
				PromptTokenCount:        100,
				CandidatesTokenCount:    50,
				TotalTokenCount:         200,
				ThoughtsTokenCount:      30,
				ToolUsePromptTokenCount: 20,
				PromptTokensDetails: []dto.GeminiPromptTokensDetails{
					{Modality: "TEXT", TokenCount: 80},
					{Modality: "AUDIO", TokenCount: 20},
				},
			},
			wantPrompt:     120, // 100 + 20 (ToolUse)
			wantCompletion: 80,  // 50 + 30 (Thoughts)
			wantTotal:      200,
			wantReasoning:  30,
			wantAudioTokens: 20,
			wantTextTokens:  80,
		},
		{
			name: "no tool use",
			metadata: dto.GeminiUsageMetadata{
				PromptTokenCount:        100,
				CandidatesTokenCount:    50,
				TotalTokenCount:         150,
				ToolUsePromptTokenCount: 0,
			},
			wantPrompt:     100,
			wantCompletion: 50,
			wantTotal:      150,
		},
		{
			name: "with thinking",
			metadata: dto.GeminiUsageMetadata{
				PromptTokenCount:     100,
				CandidatesTokenCount: 50,
				ThoughtsTokenCount:   50,
				TotalTokenCount:      200,
			},
			wantPrompt:     100,
			wantCompletion: 100, // 50 + 50
			wantTotal:      200,
			wantReasoning:  50,
		},
		{
			name: "audio only",
			metadata: dto.GeminiUsageMetadata{
				PromptTokensDetails: []dto.GeminiPromptTokensDetails{
					{Modality: "AUDIO", TokenCount: 100},
				},
			},
			wantAudioTokens: 100,
			wantTextTokens:  0,
		},
		{
			name: "text only",
			metadata: dto.GeminiUsageMetadata{
				PromptTokensDetails: []dto.GeminiPromptTokensDetails{
					{Modality: "TEXT", TokenCount: 100},
				},
			},
			wantAudioTokens: 0,
			wantTextTokens:  100,
		},
		{
			name: "unknown modality",
			metadata: dto.GeminiUsageMetadata{
				PromptTokensDetails: []dto.GeminiPromptTokensDetails{
					{Modality: "IMAGE", TokenCount: 30},
					{Modality: "VIDEO", TokenCount: 20},
				},
			},
			wantAudioTokens: 0,
			wantTextTokens:  0,
		},
		{
			name: "empty details",
			metadata: dto.GeminiUsageMetadata{
				PromptTokensDetails: []dto.GeminiPromptTokensDetails{},
			},
			wantAudioTokens: 0,
			wantTextTokens:  0,
		},
		{
			name:            "nil details",
			metadata:        dto.GeminiUsageMetadata{},
			wantAudioTokens: 0,
			wantTextTokens:  0,
		},
		{
			name:     "all zeros",
			metadata: dto.GeminiUsageMetadata{},
		},
		{
			name: "large numbers",
			metadata: dto.GeminiUsageMetadata{
				PromptTokenCount:        1_000_000,
				ToolUsePromptTokenCount: 200_000,
				CandidatesTokenCount:    500_000,
				TotalTokenCount:         1_700_000,
			},
			wantPrompt:     1_200_000,
			wantCompletion: 500_000,
			wantTotal:      1_700_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := buildUsageFromGeminiMetadata(tt.metadata)

			if usage.PromptTokens != tt.wantPrompt {
				t.Errorf("PromptTokens = %d, want %d", usage.PromptTokens, tt.wantPrompt)
			}
			if usage.CompletionTokens != tt.wantCompletion {
				t.Errorf("CompletionTokens = %d, want %d", usage.CompletionTokens, tt.wantCompletion)
			}
			if usage.TotalTokens != tt.wantTotal {
				t.Errorf("TotalTokens = %d, want %d", usage.TotalTokens, tt.wantTotal)
			}
			if usage.CompletionTokenDetails.ReasoningTokens != tt.wantReasoning {
				t.Errorf("ReasoningTokens = %d, want %d", usage.CompletionTokenDetails.ReasoningTokens, tt.wantReasoning)
			}
			if usage.PromptTokensDetails.AudioTokens != tt.wantAudioTokens {
				t.Errorf("AudioTokens = %d, want %d", usage.PromptTokensDetails.AudioTokens, tt.wantAudioTokens)
			}
			if usage.PromptTokensDetails.TextTokens != tt.wantTextTokens {
				t.Errorf("TextTokens = %d, want %d", usage.PromptTokensDetails.TextTokens, tt.wantTextTokens)
			}
		})
	}
}

// TestBuildUsageFromGeminiMetadata_RecalculationNote: the helper computes
// CompletionTokens as CandidatesTokenCount + ThoughtsTokenCount. Until
// 2026-09-02 both handlers overwrote it with TotalTokens - PromptTokens, which
// is wrong whenever toolUsePromptTokenCount is non-zero (it is inside
// PromptTokens, outside totalTokenCount); they now keep the helper figure via
// geminiCompletionTokens (tool_use_completion_test.go).
func TestBuildUsageFromGeminiMetadata_RecalculationNote(t *testing.T) {
	metadata := dto.GeminiUsageMetadata{
		PromptTokenCount:        100,
		CandidatesTokenCount:    50,
		TotalTokenCount:         200,
		ThoughtsTokenCount:      30,
		ToolUsePromptTokenCount: 20,
	}

	usage := buildUsageFromGeminiMetadata(metadata)

	// Helper calculation: CompletionTokens = Candidates + Thoughts = 50 + 30 = 80
	helperCompletion := usage.CompletionTokens
	// Caller recalculation: TotalTokens - PromptTokens = 200 - 120 = 80
	// (In this case they happen to match, but with different ToolUse values they may diverge)
	callerCompletion := metadata.TotalTokenCount - usage.PromptTokens

	t.Logf("BUG-3 documentation: helper CompletionTokens=%d, caller recalculation=%d",
		helperCompletion, callerCompletion)

	// Verify the helper's own calculation is internally consistent
	expectedCompletion := metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount
	if helperCompletion != expectedCompletion {
		t.Errorf("helper CompletionTokens = %d, want %d (Candidates + Thoughts)",
			helperCompletion, expectedCompletion)
	}
}

// ---------------------------------------------------------------------------
// GeminiModelList tests
// ---------------------------------------------------------------------------

func TestGeminiModelList_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(ModelList))
	for _, m := range ModelList {
		if seen[m] {
			t.Errorf("duplicate model in ModelList: %q", m)
		}
		seen[m] = true
	}
}

func TestGeminiModelList_ContainsExpectedModels(t *testing.T) {
	expected := []string{
		"gemini-2.0-flash",
		"gemini-1.5-pro",
		"gemini-1.5-flash",
		"imagen-3.0-generate-002",
		"text-embedding-004",
		"gemini-2.5-pro-exp-03-25",
		"gemini-2.5-flash-preview-04-17",
	}

	modelSet := make(map[string]bool, len(ModelList))
	for _, m := range ModelList {
		modelSet[m] = true
	}

	for _, want := range expected {
		if !modelSet[want] {
			t.Errorf("ModelList missing expected model %q", want)
		}
	}
}

func TestGeminiModelList_NonEmpty(t *testing.T) {
	if len(ModelList) == 0 {
		t.Error("ModelList should not be empty")
	}
}

