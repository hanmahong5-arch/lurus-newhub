package provider

import (
	"io"
	"net/http"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

type Adaptor interface {
	// Init IsStream bool
	Init(info *relaycommon.RelayInfo)
	GetRequestURL(info *relaycommon.RelayInfo) (string, error)
	SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error
	ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error)
	ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error)
	ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error)
	ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error)
	ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error)
	ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error)
	DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error)
	DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError)
	GetModelList() []string
	GetChannelName() string
	ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error)
	ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error)
}

type TaskAdaptor interface {
	Init(info *relaycommon.RelayInfo)

	ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError

	BuildRequestURL(info *relaycommon.RelayInfo) (string, error)
	BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error
	BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error)

	DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error)
	DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, err *dto.TaskError)

	GetModelList() []string
	GetChannelName() string

	// FetchTask polls a task's upstream status. forceHTTP1 is variadic so
	// existing callers/fakes that predate the L5 force_http1 pin keep
	// compiling unchanged; omitting it (or passing false) preserves the
	// pre-L5 client selection. When true, implementations must resolve their
	// http.Client via app.GetHttpClientFor(proxy, true) instead of
	// app.GetHttpClientWithProxy(proxy) — see the structural test in
	// internal/adapter/provider that enumerates provider/task/*/adaptor.go
	// and checks this.
	FetchTask(baseUrl, key string, body map[string]any, proxy string, forceHTTP1 ...bool) (*http.Response, error)

	ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error)
}

type OpenAIVideoConverter interface {
	ConvertToOpenAIVideo(originTask *repo.Task) ([]byte, error)
}
