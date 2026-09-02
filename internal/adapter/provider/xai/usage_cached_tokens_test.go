package xai

// usage_cached_tokens_test.go — xAI reports prompt_tokens_details.cached_tokens
// on the OpenAI wire, with prompt_tokens INCLUDING the cached slice (docs.x.ai
// prompt-caching usage: turn 2 prompt_tokens=120, cached_tokens=50). Before
// 2026-09-01 the two transports got this wrong in opposite directions:
//
//   - non-stream parsed cached_tokens straight into dto.Usage but never
//     stamped the wire flag, so settlement billed the full prompt PLUS the
//     cached slice again at CacheRatio;
//   - stream copied three named fields off the usage chunk and dropped
//     prompt_tokens_details entirely, so the cache hit was billed at full
//     input price and never shown to the caller.
//
// Same request, two prices, depending on `stream: true`.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

const xaiCachedUsageJSON = `"usage":{"prompt_tokens":120,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50,"text_tokens":120},"completion_tokens_details":{"reasoning_tokens":10}}`

func TestXai_NonStream_CachedTokensCarryWireFlag(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"id":"c1","model":"grok-4","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` + xaiCachedUsageJSON + `}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(w.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.PromptTokensDetails.CachedTokens != 50 {
		t.Errorf("CachedTokens = %d, want 50", u.PromptTokensDetails.CachedTokens)
	}
	if !u.PromptTokensIncludeCached {
		t.Error("PromptTokensIncludeCached = false on an OpenAI-wire usage whose prompt_tokens includes the cached slice — " +
			"settlement bills 120 at full price plus 50 at CacheRatio")
	}
}

func TestXai_Stream_CachedTokensSurviveChunkCopy(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4"},
	}
	body := `data: {"id":"c1","model":"grok-4","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"grok-4","choices":[],` + xaiCachedUsageJSON + `}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := xaiSSEBody(body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := a.DoResponse(w.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.PromptTokensDetails.CachedTokens != 50 {
		t.Errorf("stream CachedTokens = %d, want 50 — the usage chunk carried it and the copy dropped it", u.PromptTokensDetails.CachedTokens)
	}
	if u.CompletionTokenDetails.ReasoningTokens != 10 {
		t.Errorf("stream ReasoningTokens = %d, want 10 (same copy, same loss)", u.CompletionTokenDetails.ReasoningTokens)
	}
	if !u.PromptTokensIncludeCached {
		t.Error("stream usage missing PromptTokensIncludeCached")
	}
	// The money-path arithmetic this handler exists for must be unchanged.
	if u.PromptTokens != 120 || u.CompletionTokens != 30 {
		t.Errorf("prompt/completion = %d/%d, want 120/30 (=total 150 - prompt 120)", u.PromptTokens, u.CompletionTokens)
	}
	if out := w.rec.Body.String(); !strings.Contains(out, `"cached_tokens":50`) {
		t.Errorf("forwarded stream does not show the caller cached_tokens=50: %s", out)
	}
}

// Both transports must hand settlement the same usage for the same upstream
// numbers — the pre-fix divergence was a per-transport price difference.
func TestXai_UsageAgreesAcrossTransports(t *testing.T) {
	a := &Adaptor{}

	wn := newProvLongtailXaiCtx()
	nonStream, apiErr := a.DoResponse(wn.ctx, &http.Response{StatusCode: 200, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"id":"c1","model":"grok-4","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` + xaiCachedUsageJSON + `}`)),
	}, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}})
	if apiErr != nil {
		t.Fatalf("non-stream: %v", apiErr.Error())
	}

	ws := newProvLongtailXaiCtx()
	resp := xaiSSEBody(`data: {"id":"c1","model":"grok-4","choices":[],` + xaiCachedUsageJSON + `}` + "\n\ndata: [DONE]\n\n")
	defer func() { _ = resp.Body.Close() }()
	stream, apiErr := a.DoResponse(ws.ctx, resp, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4"}})
	if apiErr != nil {
		t.Fatalf("stream: %v", apiErr.Error())
	}

	n, s := nonStream.(*dto.Usage), stream.(*dto.Usage)
	type key struct {
		prompt, completion, cached, reasoning int
		includeCached                         bool
	}
	kn := key{n.PromptTokens, n.CompletionTokens, n.PromptTokensDetails.CachedTokens, n.CompletionTokenDetails.ReasoningTokens, n.PromptTokensIncludeCached}
	ks := key{s.PromptTokens, s.CompletionTokens, s.PromptTokensDetails.CachedTokens, s.CompletionTokenDetails.ReasoningTokens, s.PromptTokensIncludeCached}
	if kn != ks {
		t.Errorf("settlement input differs by transport:\n non-stream %+v\n     stream %+v", kn, ks)
	}
}
