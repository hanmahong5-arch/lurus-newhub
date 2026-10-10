package common

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ValidateRerankResponse applies the response-side rerank contract shared by
// every non-pass-through rerank handler: a self-contradictory usage block is a
// 502 (not billed, counted in metrics), documents are dropped unless the caller
// asked for them, and results must be ordered by relevance_score descending
// (502, no channel retry). A non-nil return means nothing may be written to
// the caller.
func ValidateRerankResponse(info *RelayInfo, resp *dto.RerankResponse) *types.NewAPIError {
	if dto.UsageContradictory(resp.Usage) {
		metrics.RecordInvalidProviderUsage("rerank")
		return types.NewOpenAIError(
			fmt.Errorf("upstream rerank usage is self-contradictory (prompt=%d completion=%d total=%d)",
				resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens),
			types.ErrorCodeInvalidProviderUsage, http.StatusBadGateway)
	}
	if info == nil || info.RerankerInfo == nil || !info.ReturnDocuments {
		dto.StripRerankDocuments(resp.Results)
	}
	if oerr := dto.CheckRerankOrder(resp.Results); oerr != nil {
		return types.NewOpenAIError(oerr, types.ErrorCodeInvalidProviderResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	return nil
}
