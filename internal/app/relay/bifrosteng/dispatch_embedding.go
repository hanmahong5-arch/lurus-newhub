package bifrosteng

import (
	"fmt"
	"net/http"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
)

// ShouldHandleEmbeddings is the EmbeddingHelper gate.
func ShouldHandleEmbeddings(apiType int) bool {
	return Ready() && ModeEnabled(ModeEmbeddings) && IsMapped(apiType)
}

// DispatchEmbedding runs embeddings via Bifrost and writes an OpenAI-compatible
// response. Caller settles usage via existing PostConsume paths.
func DispatchEmbedding(c *gin.Context, info *relaycommon.RelayInfo, request *dto.EmbeddingRequest) (*dto.Usage, *types.NewAPIError) {
	started := time.Now()
	acc := Account()
	cli := Client()
	if acc == nil || cli == nil {
		ObserveEngine(EngineLegacy, ModeEmbeddings, "bifrost_unready")
		return nil, types.NewError(fmt.Errorf("bifrosteng: not ready"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	providerKey, err := acc.RegisterChannel(info)
	if err != nil {
		ObserveEngine(EngineBifrost, ModeEmbeddings, "unmapped")
		return nil, types.NewError(err, types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}

	inputs := request.ParseInput()
	if len(inputs) == 0 {
		ObserveEngine(EngineBifrost, ModeEmbeddings, "bad_request")
		return nil, types.NewError(fmt.Errorf("bifrosteng: empty embedding input"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	model := info.UpstreamModelName
	if model == "" {
		model = request.Model
	}

	ein := &schemas.EmbeddingInput{}
	if len(inputs) == 1 {
		ein.Text = schemas.Ptr(inputs[0])
	} else {
		ein.Texts = inputs
	}

	bfReq := &schemas.BifrostEmbeddingRequest{
		Provider:  providerKey,
		Model:     model,
		Input:     ein,
		Fallbacks: nil,
	}

	bfCtx := schemas.NewBifrostContext(c.Request.Context(), schemas.NoDeadline)
	resp, bfErr := cli.EmbeddingRequest(bfCtx, bfReq)
	if bfErr != nil {
		observeBifrost(ModeEmbeddings, "error", started)
		ObserveEngine(EngineBifrost, ModeEmbeddings, "error")
		return nil, types.NewOpenAIError(
			fmt.Errorf("%s", bifrostErrMessage(bfErr)),
			types.ErrorCodeDoRequestFailed,
			bifrostErrStatus(bfErr),
		)
	}

	out := toOpenAIEmbeddingResponse(resp, model)
	c.JSON(http.StatusOK, out)
	observeBifrost(ModeEmbeddings, "ok", started)
	ObserveEngine(EngineBifrost, ModeEmbeddings, "ok")
	return fromBifrostUsage(resp.Usage), nil
}

func toOpenAIEmbeddingResponse(resp *schemas.BifrostEmbeddingResponse, model string) *dto.OpenAIEmbeddingResponse {
	out := &dto.OpenAIEmbeddingResponse{
		Object: "list",
		Model:  model,
		Data:   make([]dto.OpenAIEmbeddingResponseItem, 0, len(resp.Data)),
	}
	if out.Model == "" {
		out.Model = resp.Model
	}
	for _, d := range resp.Data {
		item := dto.OpenAIEmbeddingResponseItem{
			Object: "embedding",
			Index:  d.Index,
		}
		if len(d.Embedding.EmbeddingArray) > 0 {
			item.Embedding = dto.RawEmbedding(float32To64(d.Embedding.EmbeddingArray))
		}
		out.Data = append(out.Data, item)
	}
	if usage := fromBifrostUsage(resp.Usage); usage != nil {
		out.Usage = *usage
	}
	return out
}

func float32To64(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}
