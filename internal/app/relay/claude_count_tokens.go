package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/claude"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// ClaudeCountTokensHelper serves POST /v1/messages/count_tokens by proxying to
// the selected channel's upstream endpoint of the same name.
//
// WHY PROXY RATHER THAN ESTIMATE LOCALLY: callers use this number to decide
// whether to send a request at all, or how much history to drop. A local
// tokenizer estimate is systematically off for Claude (different vocabulary,
// plus untracked overhead for system prompts, tools and images), and an
// estimate that reads low turns a would-be 400 into a silent context overflow
// at the client. The endpoint is free and does not consume quota upstream, so
// there is nothing to save by guessing.
//
// NOT metered: no pre-consume, no PostConsumeQuota, no billing log. It costs
// the gateway one upstream round trip and the caller nothing, exactly matching
// the vendor's own pricing of this endpoint. Rate limiting still applies — the
// route sits behind the same TokenAuth/Distribute/rate-limit chain as
// /v1/messages, so it cannot be used as an unmetered way to hammer a channel.
func ClaudeCountTokensHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)

	claudeReq, ok := info.Request.(*dto.ClaudeRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected *dto.ClaudeRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(claudeReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to ClaudeRequest: %w", err),
			types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	// Apply the channel's model mapping so the count is computed for the model
	// that a real /v1/messages call would actually hit.
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	// Only Anthropic-protocol channels expose this endpoint. Sending it to an
	// OpenAI-compatible upstream yields an opaque upstream 404, so fail with a
	// message that names the actual problem.
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType),
			types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	if _, isClaude := adaptor.(*claude.Adaptor); !isClaude {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("model %s is served by a channel that does not implement the Anthropic count_tokens endpoint", info.OriginModelName),
			types.ErrorCodeInvalidApiType, http.StatusNotImplemented, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	body, err := json.Marshal(request)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to marshal count_tokens request: %w", err),
			types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	fullURL := fmt.Sprintf("%s/v1/messages/count_tokens", info.ChannelBaseUrl)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return types.NewError(fmt.Errorf("failed to build count_tokens request: %w", err),
			types.ErrorCodeDoRequestFailed)
	}
	req.Header.Set("Content-Type", "application/json")
	// Reuse the adaptor's header logic so auth, anthropic-version and beta flags
	// stay in one place and cannot drift from the /v1/messages path.
	if err := adaptor.SetupRequestHeader(c, &req.Header, info); err != nil {
		return types.NewError(fmt.Errorf("failed to set count_tokens headers: %w", err),
			types.ErrorCodeDoRequestFailed)
	}

	// provider.DoRequest, not a bare http.Client: it carries the channel proxy
	// setting and the SSRF dial guard that every other egress path goes through.
	resp, err := provider.DoRequest(c, req, info)
	if err != nil {
		return types.NewError(fmt.Errorf("count_tokens upstream request failed: %w", err),
			types.ErrorCodeDoRequestFailed)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to read count_tokens response: %w", err),
			types.ErrorCodeReadResponseBodyFailed)
	}

	// Pass the upstream verdict through verbatim, including its errors: the
	// caller is an Anthropic SDK that already knows how to read this shape.
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, respBody)
	return nil
}
