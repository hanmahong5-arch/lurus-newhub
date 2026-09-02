package claude

// openai_wire_prompt_semantics_test.go — an OpenAI-wire caller on an
// Anthropic-wire upstream must get prompt_tokens in OpenAI semantics.
//
// Anthropic input_tokens EXCLUDES cache_read_input_tokens and
// cache_creation_input_tokens (the three are mutually exclusive); OpenAI
// prompt_tokens INCLUDES cached_tokens (cached_tokens is "cached tokens present
// in the prompt"). Until 2026-09-02 the settlement record was copied verbatim
// into the OpenAI body, so prompt_tokens = input_tokens while cached_tokens was
// also present: an OpenAI SDK computing (prompt_tokens - cached_tokens) for its
// uncached input got input_tokens - cache_read, i.e. under-counted by the whole
// cache read (and total_tokens was short by the same amount).
//
// The fix is a display copy (dto.Usage.AsOpenAIWire) at the two OpenAI-format
// write sites in relay-claude.go. The settlement record (claudeInfo.Usage,
// what ClaudeHandler returns to settlement) keeps Anthropic semantics: the
// assertions on it below are the only lock that catches an in-place rewrite;
// the billing matrix stays green under that mutation because the flag flips
// with the figure and the charge comes out the same.
//
// Mutation oracle: revert either write site to *claudeInfo.Usage and the
// prompt_tokens assertion there reads 11 / 70; turn the copy into an in-place
// edit and the settlement assertions read 16 / 120.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestHandleClaudeResponseData_OpenAIWire_PromptTokensIncludeCacheTerms(t *testing.T) {
	c, w := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatOpenAI, "m")
	claudeInfo := fixClaudeGuardsResponseInfo()
	// input 11 / output 7 / cache read 3 / cache creation 2: Anthropic's total
	// input is 16.
	body := []byte(`{"id":"msg_2","type":"message","role":"assistant","model":"m",` +
		`"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`)

	upstream := fixClaudeGuardsUpstreamResp()
	defer func() { _ = upstream.Body.Close() }()

	if apiErr := HandleClaudeResponseData(c, info, claudeInfo, upstream, body, RequestModeMessage); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}

	var out dto.OpenAITextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("caller body is not an OpenAI response: %v\n%s", err, w.Body.String())
	}
	if out.PromptTokens != 16 || out.CompletionTokens != 7 || out.TotalTokens != 23 {
		t.Errorf("caller usage = prompt %d / completion %d / total %d, want 16 / 7 / 23 "+
			"(OpenAI prompt_tokens is the whole prompt: 11 + 3 read + 2 creation)",
			out.PromptTokens, out.CompletionTokens, out.TotalTokens)
	}
	if out.PromptTokensDetails.CachedTokens != 3 {
		t.Errorf("caller cached_tokens = %d, want 3", out.PromptTokensDetails.CachedTokens)
	}
	if strings.Contains(w.Body.String(), `"prompt_tokens":11`) {
		t.Errorf("caller body still carries the Anthropic input_tokens as prompt_tokens:\n%s", w.Body.String())
	}

	// Settlement record: Anthropic semantics, untouched by the display copy.
	if claudeInfo.Usage.PromptTokens != 11 || claudeInfo.Usage.TotalTokens != 18 || claudeInfo.Usage.PromptTokensIncludeCached {
		t.Errorf("settlement usage = prompt %d / total %d / includesCached %v, want 11 / 18 / false: "+
			"the display copy must not rewrite the record settlement prices",
			claudeInfo.Usage.PromptTokens, claudeInfo.Usage.TotalTokens, claudeInfo.Usage.PromptTokensIncludeCached)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 3 || claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 2 {
		t.Errorf("settlement cache fields = %d/%d, want 3/2", claudeInfo.Usage.PromptTokensDetails.CachedTokens, claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
}

func TestHandleStreamFinalResponse_OpenAIWire_FinalUsagePromptIncludesCacheTerms(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "m"},
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
	}
	// The record FormatClaudeResponseInfo builds from message_start +
	// message_delta for a 120-token prompt with 50 cache reads: Anthropic
	// input_tokens 70.
	claudeInfo := &ClaudeResponseInfo{
		ResponseId: "msg_s",
		Usage: &dto.Usage{
			PromptTokens:        70,
			CompletionTokens:    30,
			TotalTokens:         100,
			PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 50},
		},
		Done: true,
	}

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)

	var final *dto.ChatCompletionsStreamResponse
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
			continue
		}
		var fr dto.ChatCompletionsStreamResponse
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fr); err != nil {
			t.Fatalf("unparseable frame %q: %v", line, err)
		}
		if fr.Usage != nil {
			final = &fr
		}
	}
	if final == nil {
		t.Fatalf("no usage frame in:\n%s", w.Body.String())
	}
	if final.Usage.PromptTokens != 120 || final.Usage.TotalTokens != 150 || final.Usage.CompletionTokens != 30 {
		t.Errorf("final usage = prompt %d / completion %d / total %d, want 120 / 30 / 150 (70 + 50 cache read)",
			final.Usage.PromptTokens, final.Usage.CompletionTokens, final.Usage.TotalTokens)
	}
	if final.Usage.PromptTokensDetails.CachedTokens != 50 {
		t.Errorf("final cached_tokens = %d, want 50", final.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokens != 70 || claudeInfo.Usage.TotalTokens != 100 || claudeInfo.Usage.PromptTokensIncludeCached {
		t.Errorf("settlement usage = prompt %d / total %d / includesCached %v, want 70 / 100 / false (display copy only)",
			claudeInfo.Usage.PromptTokens, claudeInfo.Usage.TotalTokens, claudeInfo.Usage.PromptTokensIncludeCached)
	}
}

// Anthropic message_delta usage counters are cumulative whole-message totals;
// the official SDK accumulators overwrite a counter when the delta carries it
// and keep the message_start value when it does not (server-tool turns
// re-report input_tokens and both cache counters on message_delta). Before
// 2026-09-02 only input_tokens/output_tokens were taken from message_delta, so
// the cache figures stayed at their message_start values.
func TestFormatClaudeResponseInfo_MessageDeltaOverwritesCacheCountersWhenPresent(t *testing.T) {
	t.Run("delta carries the counters: overwrite, never add", func(t *testing.T) {
		claudeInfo := fixClaudeGuardsResponseInfo()
		start := &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{
			Id: "msg_t", Model: "m",
			Usage: &dto.ClaudeUsage{InputTokens: 2679, CacheReadInputTokens: 10, CacheCreationInputTokens: 5, OutputTokens: 3},
		}}
		delta := &dto.ClaudeResponse{Type: "message_delta", Usage: &dto.ClaudeUsage{
			InputTokens: 10682, CacheReadInputTokens: 200, CacheCreationInputTokens: 100, OutputTokens: 510,
		}}
		FormatClaudeResponseInfo(RequestModeMessage, start, nil, claudeInfo)
		FormatClaudeResponseInfo(RequestModeMessage, delta, nil, claudeInfo)
		u := claudeInfo.Usage
		if u.PromptTokens != 10682 || u.PromptTokensDetails.CachedTokens != 200 || u.PromptTokensDetails.CachedCreationTokens != 100 || u.CompletionTokens != 510 {
			t.Errorf("usage = prompt %d / read %d / creation %d / completion %d, want 10682 / 200 / 100 / 510",
				u.PromptTokens, u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CachedCreationTokens, u.CompletionTokens)
		}
	})
	t.Run("delta carries only output_tokens: message_start figures kept", func(t *testing.T) {
		claudeInfo := fixClaudeGuardsResponseInfo()
		start := &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{
			Id: "msg_t", Model: "m",
			Usage: &dto.ClaudeUsage{InputTokens: 70, CacheReadInputTokens: 50, CacheCreationInputTokens: 20, OutputTokens: 0},
		}}
		delta := &dto.ClaudeResponse{Type: "message_delta", Usage: &dto.ClaudeUsage{OutputTokens: 30}}
		FormatClaudeResponseInfo(RequestModeMessage, start, nil, claudeInfo)
		FormatClaudeResponseInfo(RequestModeMessage, delta, nil, claudeInfo)
		u := claudeInfo.Usage
		if u.PromptTokens != 70 || u.PromptTokensDetails.CachedTokens != 50 || u.PromptTokensDetails.CachedCreationTokens != 20 || u.CompletionTokens != 30 {
			t.Errorf("usage = prompt %d / read %d / creation %d / completion %d, want 70 / 50 / 20 / 30",
				u.PromptTokens, u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CachedCreationTokens, u.CompletionTokens)
		}
	})
}
