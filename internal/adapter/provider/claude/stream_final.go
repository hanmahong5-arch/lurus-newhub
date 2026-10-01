package claude

import (
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// HandleStreamFinalResponse settles the end of this provider's upstream stream for
// callers that only need the side effects (the AWS bedrock stream). See
// handleStreamFinalResponse for the incomplete-stream error it can surface.
func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, requestMode int) {
	_ = handleStreamFinalResponse(c, info, claudeInfo, requestMode)
}

// handleStreamFinalResponse is HandleStreamFinalResponse plus its one failure:
// when the upstream stopped without message_delta and the caller is still
// listening, the in-band error frame is written here and the same failure is
// returned (helper.SurfaceIncompleteStream) so ClaudeStreamHandler can hand it
// to the relay loop.
func handleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, requestMode int) *types.NewAPIError {

	if requestMode == RequestModeCompletion {
		claudeInfo.Usage = app.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	} else {
		if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
			if common.DebugEnabled {
				common.SysLog("claude response usage is not complete, maybe upstream error")
			}
			// ResponseText2Usage builds a FRESH usage (prompt kept, completion
			// re-estimated from the streamed text) — carry over the cache
			// dimensions already parsed from message_start, or this fallback
			// silently zeroes them and the settlement charges neither the
			// cache-read term nor the (1.25x/2x-priced) cache-creation terms.
			prev := claudeInfo.Usage
			claudeInfo.Usage = app.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, claudeInfo.Usage.PromptTokens)
			claudeInfo.Usage.PromptTokensDetails.CachedTokens = prev.PromptTokensDetails.CachedTokens
			claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = prev.PromptTokensDetails.CachedCreationTokens
			claudeInfo.Usage.ClaudeCacheCreation5mTokens = prev.ClaudeCacheCreation5mTokens
			claudeInfo.Usage.ClaudeCacheCreation1hTokens = prev.ClaudeCacheCreation1hTokens
		}
	}

	// G2 fix: the streaming counterpart of HandleClaudeResponseData's
	// c.Set("claude_web_search_requests", ...) below (non-streaming). Before
	// this, ClaudeStreamHandler -> HandleStreamFinalResponse never set this
	// key, so app.PostClaudeConsumeQuota / relay.postConsumeQuota always read
	// claude_web_search_requests==0 for a streamed /v1/messages call and a
	// Claude native web search tool call went unbilled whenever the client
	// sent stream=true. claudeInfo.WebSearchRequests survives the
	// claudeInfo.Usage reassignment above (it lives on ClaudeResponseInfo, not
	// inside *dto.Usage) so this still fires on the incomplete-usage fallback
	// path too.
	if claudeInfo.WebSearchRequests > 0 {
		c.Set("claude_web_search_requests", claudeInfo.WebSearchRequests)
	}

	// No message_delta ever arrived: the upstream stopped mid-answer. The
	// partial text is billed above (the caller received it), and the caller
	// is told it is partial — before this the OpenAI wire got a usage frame +
	// [DONE] and the Claude wire a bare EOF, both of which read as a normal
	// end. When the caller itself hung up there is nobody left to tell.
	if requestMode != RequestModeCompletion && !claudeInfo.Done && helper.ClientListening(c, info) {
		return helper.SurfaceIncompleteStream(c, info.RelayFormat, info)
	}

	// A caller on the native wire already received message_stop from the upstream;
	// only an OpenAI-wire caller needs the usage chunk and [DONE] here.
	if info.RelayFormat == types.RelayFormatOpenAI {
		if info.ShouldIncludeUsage {
			// OpenAI-wire caller: prompt_tokens must be the whole prompt with
			// cached_tokens as the subset that hit the cache (OpenAI usage
			// reference), while claudeInfo.Usage holds Anthropic input_tokens,
			// which EXCLUDES cache read/creation. AsOpenAIWire is a value copy
			// in the caller's semantics; the settlement record is untouched.
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, claudeInfo.Usage.AsOpenAIWire())
			err := helper.ObjectData(c, response)
			if err != nil {
				common.SysLog("send final response failed: " + err.Error())
			}
		}
		helper.Done(c)
	}
	return nil
}
