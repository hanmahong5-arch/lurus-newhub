package palm

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	provider.BaseAdaptor
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v1beta2/models/chat-bison-001:generateMessage", info.ChannelBaseUrl), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	provider.SetupApiRequestHeader(info, c, req)
	req.Set("x-goog-api-key", info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return provider.DoApiRequest(a, c, info, requestBody) //nolint:bodyclose // resp is handed to the relay layer, which owns closing it (DoResponse -> app.CloseResponseBodyGracefully); closing here would cut streaming bodies.
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.IsStream {
		var responseText string
		err, responseText = palmStreamHandler(c, resp)
		usage = app.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	} else {
		usage, err = palmHandler(c, info, resp)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
