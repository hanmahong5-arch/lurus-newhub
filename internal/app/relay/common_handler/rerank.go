package common_handler

import (
	"fmt"
	"io"
	"net/http"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/xinference"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// UsageContradictory is kept as the package-level entry point; the rule lives in dto.
func UsageContradictory(u dto.Usage) bool { return dto.UsageContradictory(u) }

// usageAbsent: the upstream sent no usage at all (every counter zero).
func usageAbsent(u dto.Usage) bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0
}

func checkRerankOrder(results []dto.RerankResponseResult) error {
	return dto.CheckRerankOrder(results)
}

func RerankHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	app.CloseResponseBodyGracefully(resp)
	if common.DebugEnabled {
		println("reranker response body: ", string(responseBody))
	}
	var jinaResp dto.RerankResponse
	if info.ChannelType == constant.ChannelTypeXinference {
		var xinRerankResponse xinference.XinRerankResponse
		err = common.Unmarshal(responseBody, &xinRerankResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		jinaRespResults := make([]dto.RerankResponseResult, len(xinRerankResponse.Results))
		for i, result := range xinRerankResponse.Results {
			respResult := dto.RerankResponseResult{
				Index:          result.Index,
				RelevanceScore: result.RelevanceScore,
			}
			if info.ReturnDocuments {
				var document any
				if result.Document != nil {
					if doc, ok := result.Document.(string); ok {
						if doc == "" {
							// The index used for the backfill comes from upstream, so it
							// cannot be trusted to address the caller's own document slice.
							if result.Index < 0 || result.Index >= len(info.Documents) {
								return nil, types.NewOpenAIError(
									fmt.Errorf("rerank response index %d out of range for %d documents", result.Index, len(info.Documents)),
									types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
							}
							document = info.Documents[result.Index]
						} else {
							document = doc
						}
					} else {
						document = result.Document
					}
				}
				respResult.Document = document
			}
			jinaRespResults[i] = respResult
		}
		// Xinference reports no usage: the figure is our own estimate.
		jinaResp = dto.RerankResponse{
			Results: jinaRespResults,
			Usage: dto.Usage{
				PromptTokens: info.GetEstimatePromptTokens(),
				TotalTokens:  info.GetEstimatePromptTokens(),
			},
		}
		info.UsageSource = "estimated"
	} else {
		err = common.Unmarshal(responseBody, &jinaResp)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		// Token fields keep the upstream's own values (Jina reports only
		// total_tokens): copying total into prompt used to fabricate a figure
		// the vendor never sent. Settlement reads total_tokens when it is the
		// only counter present.
		if UsageContradictory(jinaResp.Usage) {
			metrics.RecordInvalidProviderUsage("rerank")
			return nil, types.NewOpenAIError(
				fmt.Errorf("upstream rerank usage is self-contradictory (prompt=%d completion=%d total=%d)",
					jinaResp.Usage.PromptTokens, jinaResp.Usage.CompletionTokens, jinaResp.Usage.TotalTokens),
				types.ErrorCodeInvalidProviderUsage, http.StatusBadGateway)
		}
		if usageAbsent(jinaResp.Usage) {
			est := info.GetEstimatePromptTokens()
			jinaResp.Usage.PromptTokens = est
			jinaResp.Usage.TotalTokens = est
			info.UsageSource = "estimated"
		} else {
			info.UsageSource = "upstream"
		}
		if info.RerankerInfo == nil || !info.ReturnDocuments {
			for i := range jinaResp.Results {
				jinaResp.Results[i].Document = nil
			}
		}
	}
	if oerr := checkRerankOrder(jinaResp.Results); oerr != nil {
		return nil, types.NewOpenAIError(oerr, types.ErrorCodeInvalidProviderResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.JSON(http.StatusOK, jinaResp)
	return &jinaResp.Usage, nil
}
