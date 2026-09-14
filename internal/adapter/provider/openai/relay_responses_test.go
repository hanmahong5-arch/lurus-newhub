package openai

// relay_responses_test.go — business-acceptance tests for
// OaiResponsesCompactHandler (POST /v1/responses/compact, cycle-8 L6,
// wire-formats-03): usage is parsed for billing when the vendor sends it,
// falls back to the pre-request estimate when it doesn't, and the response
// body is always forwarded to the caller verbatim regardless of which.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// TestOaiResponsesHandler_StashesResponseId is the oracle for cycle-8 L7's
// (tasks-plugins-12) insert-hook prerequisite: OaiResponsesHandler must
// c.Set("responses_id", ...) the vendor id from the JSON body so
// relay.ResponsesHelper's post-consume hook has something to write into
// response_registry. The mutation "drop the ID stash" makes this red.
func TestOaiResponsesHandler_StashesResponseId(t *testing.T) {
	t.Run("id present", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		body := `{"id":"resp_stash_1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

		if _, apiErr := OaiResponsesHandler(w.ctx, info, resp); apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		got, exists := w.ctx.Get("responses_id")
		if !exists {
			t.Fatal("responses_id was not stashed in the gin context")
		}
		if got != "resp_stash_1" {
			t.Errorf("responses_id = %v, want %q", got, "resp_stash_1")
		}
	})

	t.Run("id absent leaves nothing stashed", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		body := `{"status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

		if _, apiErr := OaiResponsesHandler(w.ctx, info, resp); apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		if _, exists := w.ctx.Get("responses_id"); exists {
			t.Error("responses_id was stashed despite an empty id in the vendor body")
		}
	})
}

func TestOaiResponsesCompactHandler_UsageParsedElseEstimated(t *testing.T) {
	t.Run("usage present", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		body := `{"id":"resp1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":2}}}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

		usage, apiErr := OaiResponsesCompactHandler(w.ctx, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		if usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
			t.Errorf("usage = %+v, want prompt=10 completion=5 total=15", usage)
		}
		if usage.PromptTokensDetails.CachedTokens != 2 {
			t.Errorf("CachedTokens = %d, want 2", usage.PromptTokensDetails.CachedTokens)
		}
		if w.rec.Body.String() != body {
			t.Errorf("response body should be forwarded verbatim, got %q", w.rec.Body.String())
		}
	})

	t.Run("usage absent falls back to estimate", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		info.SetEstimatePromptTokens(42)
		body := `{"id":"resp2","status":"completed","output":[]}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

		usage, apiErr := OaiResponsesCompactHandler(w.ctx, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		if usage.PromptTokens != 42 {
			t.Errorf("PromptTokens = %d, want 42 (the pre-request estimate)", usage.PromptTokens)
		}
		if usage.CompletionTokens != 0 {
			t.Errorf("CompletionTokens = %d, want 0 (no way to reconstruct output tokens from an opaque body)", usage.CompletionTokens)
		}
		if w.rec.Body.String() != body {
			t.Errorf("response body should be forwarded verbatim even with no usage, got %q", w.rec.Body.String())
		}
	})

	t.Run("malformed body still forwarded, falls back to estimate", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		info.SetEstimatePromptTokens(7)
		body := `{not-json`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

		usage, apiErr := OaiResponsesCompactHandler(w.ctx, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error for malformed body — the endpoint's contract is pass-through, not validation: %v", apiErr.Error())
		}
		if usage.PromptTokens != 7 {
			t.Errorf("PromptTokens = %d, want 7 (the pre-request estimate)", usage.PromptTokens)
		}
		if w.rec.Body.String() != body {
			t.Errorf("malformed body should still be forwarded verbatim, got %q", w.rec.Body.String())
		}
	})

	// TestOaiResponsesCompactHandler_UsageParsedElseEstimated/vendor error
	// in-band inside a 200 is the oracle for repair-round finding B-F1: a
	// vendor error carried inside a 200 body must be refused as an upstream
	// error and never billed at the estimate, instead of being forwarded and
	// billed. Mirrors OaiResponsesHandler's GetOpenAIError guard.
	t.Run("vendor error in-band inside a 200 is refused, not billed", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		info.SetEstimatePromptTokens(42)
		body := `{"error":{"type":"server_error","message":"boom"}}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

		usage, apiErr := OaiResponsesCompactHandler(w.ctx, info, resp)
		if apiErr == nil {
			t.Fatal("expected a typed error for an in-band vendor error, got nil")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil (an in-band error must never be billed)", usage)
		}
		if w.rec.Body.String() != "" {
			t.Errorf("body should not be forwarded when the call is refused as an error, got %q", w.rec.Body.String())
		}
	})
}

// TestAdaptor_GetRequestURL_ResponsesCompactAzureRefused is the oracle for
// A-F4: the Azure GetRequestURL branch for RelayModeResponsesCompact
// (adaptor.go's "responses/compact is not supported on Azure channels"
// refusal) is otherwise deletable-green — nothing else in the package
// reaches it.
func TestAdaptor_GetRequestURL_ResponsesCompactAzureRefused(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeAzure,
			ChannelBaseUrl: "https://example.openai.azure.com",
		},
		RelayMode:      relayconstant.RelayModeResponsesCompact,
		RequestURLPath: "/v1/responses/compact",
	}
	url, err := a.GetRequestURL(info)
	if err == nil {
		t.Fatalf("expected an error refusing /v1/responses/compact on an Azure channel, got url=%q", url)
	}
}

// TestOaiResponsesStreamHandler_StashesResponseIdBeforeCompletion is the
// oracle for cycle-8 L7 repair round findings B-F4/A-F2: the response id
// must be stashed on the FIRST event that carries one, not only on
// "response.completed". A background+stream client that reads an early
// event and then loses the connection before "response.completed" ever
// arrives must still leave a registry-insertable id behind — the stream
// below never reaches "response.completed" at all.
func TestOaiResponsesStreamHandler_StashesResponseIdBeforeCompletion(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"}}
	// Stream ends after "response.created" — no "response.completed" event
	// at all, simulating a connection lost mid-background-generation.
	body := `data: {"type":"response.created","response":{"id":"resp_early_stash","status":"in_progress"}}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	if _, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	got, exists := w.ctx.Get("responses_id")
	if !exists {
		t.Fatal("responses_id was not stashed — the mutation 'only stash on response.completed' makes this red")
	}
	if got != "resp_early_stash" {
		t.Errorf("responses_id = %v, want %q", got, "resp_early_stash")
	}
}
