package systemone

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// upstreamUsage is what the upstream reports. Pointers tell a missing count
// from a real zero: a missing usage object must fall back to an estimate, a
// reported zero (empty questions) must stay zero.
type upstreamUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
	Truncated    bool `json:"truncated"`
}

// DoResponse reads a 200 reply, normalises it to the hosted wire shape and
// writes it to the caller. It returns *dto.Usage: only the input side is
// billed (CompletionTokens stays 0), matching how the product is priced.
//
// The body is rebuilt rather than proxied, so upstream-only fields (routing,
// usage extras) and upstream identifiers (request ids, edge headers) never
// reach the caller, and the caller's own x-request-id survives.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, badUpstream(errors.New("systemone upstream returned no response"), types.ErrorCodeBadResponse)
	}
	defer app.CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, badUpstream(fmt.Errorf("systemone: read upstream response: %w", err), types.ErrorCodeReadResponseBodyFailed)
	}
	if len(body) > maxResponseBytes {
		return nil, badUpstream(fmt.Errorf("systemone upstream response exceeds %d bytes", maxResponseBytes), types.ErrorCodeBadResponseBody)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, badUpstream(errors.New("systemone upstream response is not valid JSON"), types.ErrorCodeBadResponseBody)
	}
	var answers map[string]json.RawMessage
	if raw := bytes.TrimSpace(top["answers"]); len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &answers) != nil || answers == nil {
		return nil, badUpstream(errors.New("systemone upstream response has no \"answers\" object"), types.ErrorCodeBadResponseBody)
	}

	var upstreamModel string
	if raw, ok := top["model"]; ok {
		// A non-string model is ignored rather than failing an otherwise
		// valid answer.
		if json.Unmarshal(raw, &upstreamModel) != nil {
			upstreamModel = ""
		}
	}

	usage := normalizeUsage(c, top["usage"], info)

	out := dto.SystemOneResponse{Model: responseModel(info, upstreamModel), Answers: answers, Usage: usage}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // answers pass through verbatim, including < > &
	if err := enc.Encode(out); err != nil {
		return nil, badUpstream(fmt.Errorf("systemone: encode response: %w", err), types.ErrorCodeJsonMarshalFailed)
	}
	c.Data(http.StatusOK, "application/json", bytes.TrimRight(buf.Bytes(), "\n"))

	return &dto.Usage{PromptTokens: usage.InputTokens, CompletionTokens: 0, TotalTokens: usage.InputTokens}, nil
}

// responseModel: the hosted API reports the versioned model it ran, which the
// caller should see; a self-hosted server reports an internal checkpoint name
// that means nothing to the caller, so they get the public name they asked for.
func responseModel(info *relaycommon.RelayInfo, upstreamModel string) string {
	if info.ChannelType == constant.ChannelTypeSystemOneCompatible || upstreamModel == "" {
		return info.OriginModelName
	}
	return upstreamModel
}

func normalizeUsage(c *gin.Context, raw json.RawMessage, info *relaycommon.RelayInfo) dto.SystemOneUsage {
	var up upstreamUsage
	if len(raw) == 0 || json.Unmarshal(raw, &up) != nil || up.InputTokens == nil {
		estimate := info.GetEstimatePromptTokens()
		logger.LogWarn(c, fmt.Sprintf("systemone upstream reply has no usable usage; billing the %d-token estimate", estimate))
		return dto.SystemOneUsage{InputTokens: estimate, OutputTokens: nonNegative(up.OutputTokens), Truncated: up.Truncated}
	}
	return dto.SystemOneUsage{
		InputTokens:  max(*up.InputTokens, 0),
		OutputTokens: nonNegative(up.OutputTokens),
		Truncated:    up.Truncated,
	}
}

func nonNegative(v *int) int {
	if v == nil || *v < 0 {
		return 0
	}
	return *v
}

// badUpstream is a malformed 200: the upstream is at fault, so it is a 502 that
// is retried elsewhere and charged to the breaker.
func badUpstream(err error, code types.ErrorCode) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, code, http.StatusBadGateway)
}
