package openai

// cache_write_tokens_test.go — OpenAI's cache_write_tokens must reach the
// ledger and the caller on every transport that carries it.
//
// The field exists on both usage shapes of the OpenAI wire (chat
// prompt_tokens_details, Responses input_tokens_details): "the unadjusted
// number of prompt tokens written to cache", disjoint from cached_tokens and
// inside prompt_tokens. Until 2026-09-02 dto.InputTokenDetails hid the Go field
// from JSON, so a GPT-5.6 cache write (priced 1.25x by the vendor) was billed as
// plain input, and a Claude-wire caller on an OpenAI upstream never saw a
// cache_creation_input_tokens figure it could reconcile.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const chatUsageWithCacheWrite = `"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50,"cache_write_tokens":20}}`

func chatBodyWithCacheWrite() string {
	return `{"id":"chatcmpl-1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` + chatUsageWithCacheWrite + `}`
}

func chatStreamWithCacheWrite() string {
	return "data: " + `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: " + `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[],` + chatUsageWithCacheWrite + `}` + "\n\n" +
		"data: [DONE]\n\n"
}

func assertCacheWriteLedger(t *testing.T, tag string, usage *dto.Usage) {
	t.Helper()
	if usage == nil {
		t.Fatalf("%s: nil usage", tag)
	}
	if usage.PromptTokens != 120 || usage.PromptTokensDetails.CachedTokens != 50 || usage.PromptTokensDetails.CachedCreationTokens != 20 {
		t.Errorf("%s: ledger prompt/cached/write = %d/%d/%d, want 120/50/20", tag,
			usage.PromptTokens, usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CachedCreationTokens)
	}
	if !usage.PromptTokensIncludeCached {
		t.Errorf("%s: wire flag not stamped; settlement would bill 120 at full price plus the cache terms", tag)
	}
}

func TestOpenaiHandler_CacheWriteTokens_LedgerAndOpenAICaller(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	resp := fakeHTTPResponse(200, chatBodyWithCacheWrite())
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	assertCacheWriteLedger(t, "chat non-stream", usage)
	if !strings.Contains(w.rec.Body.String(), `"cache_write_tokens":20`) {
		t.Errorf("OpenAI-wire caller lost cache_write_tokens: %s", w.rec.Body.String())
	}
}

func TestOpenaiHandler_CacheWriteTokens_ClaudeCallerSeesCacheCreation(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
		RelayFormat:       types.RelayFormatClaude,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
	}
	resp := fakeHTTPResponse(200, chatBodyWithCacheWrite())
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	assertCacheWriteLedger(t, "chat non-stream claude caller", usage)

	var out dto.ClaudeResponse
	if err := json.Unmarshal(w.rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("caller body is not a Claude message: %v\n%s", err, w.rec.Body.String())
	}
	if out.Usage == nil {
		t.Fatalf("no usage in Claude body: %s", w.rec.Body.String())
	}
	// Anthropic semantics: input_tokens excludes both cache terms.
	if out.Usage.InputTokens != 50 || out.Usage.CacheReadInputTokens != 50 || out.Usage.CacheCreationInputTokens != 20 {
		t.Errorf("Claude caller usage = input %d / read %d / creation %d, want 50 / 50 / 20",
			out.Usage.InputTokens, out.Usage.CacheReadInputTokens, out.Usage.CacheCreationInputTokens)
	}
}

func TestOaiStreamHandler_CacheWriteTokens_LedgerAndCallers(t *testing.T) {
	prev := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	defer func() { constant.StreamingTimeout = prev }()

	t.Run("openai caller", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
			RelayFormat:        types.RelayFormatOpenAI,
			IsStream:           true,
			ShouldIncludeUsage: true,
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(chatStreamWithCacheWrite())), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
		usage, apiErr := OaiStreamHandler(w.ctx, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		assertCacheWriteLedger(t, "chat stream", usage)
		if !strings.Contains(w.rec.Body.String(), `"cache_write_tokens":20`) {
			t.Errorf("OpenAI-wire stream caller lost cache_write_tokens:\n%s", w.rec.Body.String())
		}
	})

	t.Run("claude caller", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
			RelayFormat:        types.RelayFormatClaude,
			IsStream:           true,
			ShouldIncludeUsage: true,
			ClaudeConvertInfo:  &relaycommon.ClaudeConvertInfo{},
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(chatStreamWithCacheWrite())), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
		usage, apiErr := OaiStreamHandler(w.ctx, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		assertCacheWriteLedger(t, "chat stream claude caller", usage)
		body := w.rec.Body.String()
		if !strings.Contains(body, `"cache_creation_input_tokens":20`) || !strings.Contains(body, `"cache_read_input_tokens":50`) {
			t.Errorf("Claude-wire stream caller did not get the cache terms in message_delta:\n%s", body)
		}
	})
}

func TestOaiResponsesHandler_CacheWriteTokensReachTheLedger(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"id":"resp1","status":"completed","output":[],"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"input_tokens_details":{"cached_tokens":50,"cache_write_tokens":20}}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	assertCacheWriteLedger(t, "responses non-stream", usage)
}

func TestOaiResponsesStreamHandler_CacheWriteTokensReachTheLedger(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"}}
	body := `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"r1","status":"completed","output":[],"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"input_tokens_details":{"cached_tokens":50,"cache_write_tokens":20}}}}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	assertCacheWriteLedger(t, "responses stream", usage)
}
