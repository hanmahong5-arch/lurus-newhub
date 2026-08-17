package jina

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openai"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/common_handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	provider.BaseAdaptor
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode == constant.RelayModeRerank {
		return fmt.Sprintf("%s/v1/rerank", info.ChannelBaseUrl), nil
	} else if info.RelayMode == constant.RelayModeEmbeddings {
		return fmt.Sprintf("%s/v1/embeddings", info.ChannelBaseUrl), nil
	}
	return "", errors.New("invalid relay mode")
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	provider.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return provider.DoApiRequest(a, c, info, requestBody) //nolint:bodyclose // resp is handed to the relay layer, which owns closing it (DoResponse -> app.CloseResponseBodyGracefully); closing here would cut streaming bodies.
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	request.EncodingFormat = ""
	return request, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == constant.RelayModeRerank {
		usage, err = common_handler.RerankHandler(c, info, resp)
	} else if info.RelayMode == constant.RelayModeEmbeddings {
		usage, err = openai.OpenaiHandler(c, info, resp)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
