package openai

// The non-streaming handler hands the assistant text to the opt-in body
// archive through the request context (migration 052). An upstream error body
// must stash nothing, so a failed call never archives a stale/empty response.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestOpenaiHandler_StashesResponseTextForBodyArchive(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: "openai",
	}
	body := `{"id":"c1","model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"first"}},{"index":1,"message":{"role":"assistant","content":"second"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	resp := fakeHTTPResponse(200, body)
	defer resp.Body.Close()

	if _, apiErr := OpenaiHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	got, ok := w.ctx.Get(string(constant.ContextKeyResponseText))
	if !ok || got != "first\n\nsecond" {
		t.Fatalf("stashed = %#v (present=%v), want the joined choice texts", got, ok)
	}
}

func TestOpenaiHandler_UpstreamErrorStashesNothing(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: "openai",
	}
	resp := fakeHTTPResponse(401, `{"error":{"message":"bad key","type":"invalid_request_error","code":"invalid_api_key"}}`)
	defer resp.Body.Close()

	if _, apiErr := OpenaiHandler(w.ctx, info, resp); apiErr == nil {
		t.Fatal("expected an error")
	}
	if _, ok := w.ctx.Get(string(constant.ContextKeyResponseText)); ok {
		t.Fatal("an upstream error must not stash a response text")
	}
}
