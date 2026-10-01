package gemini

// stream_failover_test.go — a vendor-a stream that ends without a finishReason
// BEFORE any byte reached the caller must fail over: retryable error, nothing
// written, nothing billed. Once frames have gone out the in-band error stays
// (stream_incomplete_test.go).

import (
	"net/http"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestGeminiStreams_IncompleteBeforeFirstByte_FailsOver(t *testing.T) {
	bodies := map[string]string{
		"empty body":             "",
		"keepalive comment only": ": keepalive\n\n",
	}
	type handler func(t *testing.T, body string) (usage *dto.Usage, apiErr *types.NewAPIError, written bool, out string)
	chat := func(format types.RelayFormat) handler {
		return func(t *testing.T, body string) (*dto.Usage, *types.NewAPIError, bool, string) {
			c, w := newHandlerContext()
			info := &relaycommon.RelayInfo{
				ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "model-a"},
				RelayFormat:        format,
				ShouldIncludeUsage: true,
				ClaudeConvertInfo:  &relaycommon.ClaudeConvertInfo{},
			}
			resp := respFromBody(200, body)
			defer func() { _ = resp.Body.Close() }()
			usage, apiErr := GeminiChatStreamHandler(c, info, resp)
			return usage, apiErr, c.Writer.Written(), w.Body.String()
		}
	}
	native := func(t *testing.T, body string) (*dto.Usage, *types.NewAPIError, bool, string) {
		c, w := newHandlerContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "model-a"},
			RelayFormat: types.RelayFormatGemini,
		}
		resp := respFromBody(200, body)
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := GeminiTextGenerationStreamHandler(c, info, resp)
		return usage, apiErr, c.Writer.Written(), w.Body.String()
	}
	handlers := map[string]handler{
		"chat/openai": chat(types.RelayFormatOpenAI),
		"chat/claude": chat(types.RelayFormatClaude),
		"native":      native,
	}
	for hname, h := range handlers {
		for bname, body := range bodies {
			t.Run(hname+"/"+bname, func(t *testing.T) {
				usage, apiErr, written, out := h(t, body)
				if apiErr == nil {
					t.Fatalf("incomplete stream with nothing delivered must return an error (nil is recorded as breaker success and never fails over); body=%q", out)
				}
				if types.IsSkipRetryError(apiErr) || types.IsChannelError(apiErr) || !types.IsUpstreamFailure(apiErr) || apiErr.StatusCode != http.StatusBadGateway {
					t.Errorf("error must be a retryable 502 upstream failure: skipRetry=%v channel=%v upstream=%v status=%d",
						types.IsSkipRetryError(apiErr), types.IsChannelError(apiErr), types.IsUpstreamFailure(apiErr), apiErr.StatusCode)
				}
				if written || out != "" {
					t.Errorf("nothing may be written when failing over: %q", out)
				}
				if usage != nil && (usage.TotalTokens != 0 || usage.PromptTokens != 0 || usage.CompletionTokens != 0) {
					t.Errorf("nothing delivered, nothing billed: usage = %+v", usage)
				}
			})
		}
	}
}
