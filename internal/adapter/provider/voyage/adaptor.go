// Package voyage adapts the Voyage AI embeddings and rerank endpoints. Both
// speak near-OpenAI/Jina wire formats but rename a few fields, which is
// exactly where a pass-through adaptor would silently drop caller intent.
package voyage

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openai"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	provider.BaseAdaptor
}

type rerankRequest struct {
	Model           string `json:"model"`
	Query           string `json:"query"`
	Documents       []any  `json:"documents"`
	TopK            int    `json:"top_k,omitempty"`
	ReturnDocuments *bool  `json:"return_documents,omitempty"`
}

type embeddingRequest struct {
	Model           string `json:"model"`
	Input           any    `json:"input"`
	InputType       string `json:"input_type,omitempty"`
	OutputDimension int    `json:"output_dimension,omitempty"`
	EncodingFormat  string `json:"encoding_format,omitempty"`
}

type rerankResponse struct {
	Data []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
		Document       any     `json:"document,omitempty"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	switch info.RelayMode {
	case constant.RelayModeRerank:
		return fmt.Sprintf("%s/v1/rerank", info.ChannelBaseUrl), nil
	case constant.RelayModeEmbeddings:
		return fmt.Sprintf("%s/v1/embeddings", info.ChannelBaseUrl), nil
	}
	return "", errors.New("invalid relay mode")
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	provider.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return provider.DoApiRequest(a, c, info, requestBody) //nolint:bodyclose // resp is handed to the relay layer, which owns closing it.
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return rerankRequest{
		Model: request.Model, Query: request.Query, Documents: request.Documents,
		TopK: request.TopN, ReturnDocuments: request.ReturnDocuments,
	}, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	out := embeddingRequest{
		Model: request.Model, Input: request.Input, InputType: request.InputType,
		OutputDimension: request.Dimensions,
	}
	// Voyage only accepts "base64" here; its default already is float.
	if request.EncodingFormat == "base64" {
		out.EncodingFormat = "base64"
	}
	return out, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case constant.RelayModeRerank:
		return doRerank(c, resp, info)
	case constant.RelayModeEmbeddings:
		return openai.OpenaiHandler(c, info, resp)
	}
	return nil, types.NewError(errors.New("invalid relay mode"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
}

func doRerank(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	body, rerr := io.ReadAll(resp.Body)
	app.CloseResponseBodyGracefully(resp)
	if rerr != nil {
		return nil, types.NewOpenAIError(rerr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	var vr rerankResponse
	if uerr := common.Unmarshal(body, &vr); uerr != nil {
		return nil, types.NewOpenAIError(uerr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	out := dto.RerankResponse{Results: make([]dto.RerankResponseResult, len(vr.Data))}
	for i, d := range vr.Data {
		out.Results[i] = dto.RerankResponseResult{Document: d.Document, Index: d.Index, RelevanceScore: d.RelevanceScore}
	}
	// Prompt mirrors total because rerank billing reads prompt tokens today.
	out.Usage = dto.Usage{PromptTokens: vr.Usage.TotalTokens, TotalTokens: vr.Usage.TotalTokens}
	if verr := relaycommon.ValidateRerankResponse(info, &out); verr != nil {
		return nil, verr
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.JSON(http.StatusOK, out)
	return &out.Usage, nil
}

func (a *Adaptor) GetModelList() []string { return ModelList }

func (a *Adaptor) GetChannelName() string { return ChannelName }
