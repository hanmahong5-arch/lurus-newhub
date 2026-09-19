package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type testResult struct {
	context     *gin.Context
	localErr    error
	newAPIError *types.NewAPIError
}

// channelProbeOptions controls the side effects that differ between
// probeChannel's callers. RecordConsumeLog is the oldest of them: the manual
// GET /api/channel/test/:id path (TestChannel below, via testChannelForActor)
// still writes a real consume-log row; the automatic pass (autoProbeChannel,
// channel_probe_policy.go) does not — every automatic probe used to write one
// as user 1, polluting the leaderboard and quota_data (cycle-11 plan §L3).
// Both paths go through metrics.RecordChannelProbe regardless of this flag.
type channelProbeOptions struct {
	RecordConsumeLog bool
	// ActorUserID / TenantID attribute the consume-log row this probe
	// writes (they are read only under RecordConsumeLog) to the operator
	// who asked for the probe and to that operator's tenant. probeChannel
	// builds its own gin.Context and loads user 1 into it for the relay
	// identity, so without these the row was written as user 1 in whatever
	// tenant resolveLogTenantID (repo/log.go) picked for user 1: the
	// operator saw nothing in their own log list and another tenant got a
	// row it did not ask for. ActorUserID 0 keeps that older attribution,
	// for callers that have no actor.
	ActorUserID int
	TenantID    string
}

// testChannel is the no-actor form of the manual probe, kept for
// context_tier_channel_test_test.go's direct call. It is probeChannel with
// RecordConsumeLog on — the one probe path this cycle's plan keeps writing a
// consume-log row — and no attribution, so its row lands on user 1 the way it
// always did. The HTTP handler (TestChannel below) uses testChannelForActor
// instead.
func testChannel(channel *repo.Channel, testModel string, endpointType string) testResult {
	return probeChannel(channel, testModel, endpointType, channelProbeOptions{RecordConsumeLog: true})
}

// testChannelForActor is testChannel with the requesting operator attached, so
// the consume-log row belongs to them and to their tenant.
func testChannelForActor(channel *repo.Channel, testModel string, endpointType string, actorUserID int, tenantID string) testResult {
	return probeChannel(channel, testModel, endpointType, channelProbeOptions{
		RecordConsumeLog: true,
		ActorUserID:      actorUserID,
		TenantID:         tenantID,
	})
}

func probeChannel(channel *repo.Channel, testModel string, endpointType string, opts channelProbeOptions) testResult {
	tik := time.Now()
	var unsupportedTestChannelTypes = []int{
		constant.ChannelTypeMidjourney,
		constant.ChannelTypeMidjourneyPlus,
		constant.ChannelTypeSunoAPI,
		constant.ChannelTypeKling,
		constant.ChannelTypeJimeng,
		constant.ChannelTypeDoubaoVideo,
		constant.ChannelTypeVidu,
	}
	if lo.Contains(unsupportedTestChannelTypes, channel.Type) {
		channelTypeName := constant.GetChannelTypeName(channel.Type)
		return testResult{
			localErr: fmt.Errorf("%s channel test is not supported", channelTypeName),
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testModel = strings.TrimSpace(testModel)
	if testModel == "" {
		if channel.TestModel != nil && *channel.TestModel != "" {
			testModel = strings.TrimSpace(*channel.TestModel)
		} else {
			models := channel.GetModels()
			if len(models) > 0 {
				testModel = strings.TrimSpace(models[0])
			}
			if testModel == "" {
				testModel = "gpt-4o-mini"
			}
		}
	}

	requestPath := "/v1/chat/completions"

	// 如果指定了端点类型，使用指定的端点类型
	if endpointType != "" {
		if endpointInfo, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType)); ok {
			requestPath = endpointInfo.Path
		}
	} else {
		// 如果没有指定端点类型，使用原有的自动检测逻辑
		// 先判断是否为 Embedding 模型
		if strings.Contains(strings.ToLower(testModel), "embedding") ||
			strings.HasPrefix(testModel, "m3e") || // m3e 系列模型
			strings.Contains(testModel, "bge-") || // bge 系列模型
			strings.Contains(testModel, "embed") ||
			channel.Type == constant.ChannelTypeMokaAI { // 其他 embedding 模型
			requestPath = "/v1/embeddings" // 修改请求路径
		}

		// VolcEngine 图像生成模型
		if channel.Type == constant.ChannelTypeVolcEngine && strings.Contains(testModel, "seedream") {
			requestPath = "/v1/images/generations"
		}

		// responses-only models
		if strings.Contains(strings.ToLower(testModel), "codex") {
			requestPath = "/v1/responses"
		}
	}

	c.Request = &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: requestPath}, // 使用动态路径
		Body:   nil,
		Header: make(http.Header),
	}

	// Deadline (cycle-11 plan §L3): the raw *http.Request built above has no
	// context, so every c.Request.Context() call downstream (relay's
	// provider.DoApiRequest) resolved to context.Background() — no deadline
	// of its own. The shared relay transport already bounds two narrower
	// cases (RelayResponseHeaderTimeout, 90s to the first response header,
	// internal/pkg/common/init.go; and a 300s trickling-body read timeout,
	// internal/app/http_client.go), but nothing bounded the combination
	// end-to-end (the 2026-09-17 audit's field note records one probe
	// against a hung upstream running about 900s).
	// channelProbeTimeout() adds a single budget for the whole probe,
	// including the body read, and is re-read on every call so an
	// operator's env change takes effect without a restart.
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), channelProbeTimeout())
	defer cancelProbe()
	c.Request = c.Request.WithContext(probeCtx)

	cache, err := repo.GetUserCache(1)
	if err != nil {
		return testResult{
			localErr:    err,
			newAPIError: nil,
		}
	}
	repo.UserBaseWriteContext(cache, c)

	//c.Request.Header.Set("Authorization", "Bearer "+channel.Key)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	group, _ := repo.GetUserGroup(1, false)
	c.Set("group", group)

	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, testModel)
	if newAPIError != nil {
		return testResult{
			context:     c,
			localErr:    newAPIError,
			newAPIError: newAPIError,
		}
	}

	// Determine relay format based on endpoint type or request path
	var relayFormat types.RelayFormat
	if endpointType != "" {
		// 根据指定的端点类型设置 relayFormat
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeOpenAI:
			relayFormat = types.RelayFormatOpenAI
		case constant.EndpointTypeOpenAIResponse:
			relayFormat = types.RelayFormatOpenAIResponses
		case constant.EndpointTypeAnthropic:
			relayFormat = types.RelayFormatClaude
		case constant.EndpointTypeGemini:
			relayFormat = types.RelayFormatGemini
		case constant.EndpointTypeJinaRerank:
			relayFormat = types.RelayFormatRerank
		case constant.EndpointTypeImageGeneration:
			relayFormat = types.RelayFormatOpenAIImage
		case constant.EndpointTypeEmbeddings:
			relayFormat = types.RelayFormatEmbedding
		default:
			relayFormat = types.RelayFormatOpenAI
		}
	} else {
		// 根据请求路径自动检测
		relayFormat = types.RelayFormatOpenAI
		if c.Request.URL.Path == "/v1/embeddings" {
			relayFormat = types.RelayFormatEmbedding
		}
		if c.Request.URL.Path == "/v1/images/generations" {
			relayFormat = types.RelayFormatOpenAIImage
		}
		if c.Request.URL.Path == "/v1/messages" {
			relayFormat = types.RelayFormatClaude
		}
		if strings.Contains(c.Request.URL.Path, "/v1beta/models") {
			relayFormat = types.RelayFormatGemini
		}
		if c.Request.URL.Path == "/v1/rerank" || c.Request.URL.Path == "/rerank" {
			relayFormat = types.RelayFormatRerank
		}
		if c.Request.URL.Path == "/v1/responses" {
			relayFormat = types.RelayFormatOpenAIResponses
		}
	}

	request := buildTestRequest(testModel, endpointType, channel)

	info, err := relaycommon.GenRelayInfo(c, relayFormat, request, nil)

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeGenRelayInfoFailed),
		}
	}

	info.InitChannelMeta(c)

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeChannelModelMappedError),
		}
	}

	testModel = info.UpstreamModelName
	// 更新请求中的模型名称
	request.SetModelName(testModel)

	apiType, _ := common.ChannelType2APIType(channel.Type)
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return testResult{
			context:     c,
			localErr:    fmt.Errorf("invalid api type: %d, adaptor is nil", apiType),
			newAPIError: types.NewError(fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), types.ErrorCodeInvalidApiType),
		}
	}

	//// 创建一个用于日志的 info 副本，移除 ApiKey
	//logInfo := info
	//logInfo.ApiKey = ""
	common.SysLog(fmt.Sprintf("testing channel %d with model %s , info %+v ", channel.Id, testModel, info.ToString()))

	priceData, err := helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeModelPriceError),
		}
	}

	adaptor.Init(info)

	var convertedRequest any
	// 根据 RelayMode 选择正确的转换函数
	switch info.RelayMode {
	case relayconstant.RelayModeEmbeddings:
		// Embedding 请求 - request 已经是正确的类型
		if embeddingReq, ok := request.(*dto.EmbeddingRequest); ok {
			convertedRequest, err = adaptor.ConvertEmbeddingRequest(c, info, *embeddingReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid embedding request type"),
				newAPIError: types.NewError(errors.New("invalid embedding request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeImagesGenerations:
		// 图像生成请求 - request 已经是正确的类型
		if imageReq, ok := request.(*dto.ImageRequest); ok {
			convertedRequest, err = adaptor.ConvertImageRequest(c, info, *imageReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid image request type"),
				newAPIError: types.NewError(errors.New("invalid image request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeRerank:
		// Rerank 请求 - request 已经是正确的类型
		if rerankReq, ok := request.(*dto.RerankRequest); ok {
			convertedRequest, err = adaptor.ConvertRerankRequest(c, info.RelayMode, *rerankReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid rerank request type"),
				newAPIError: types.NewError(errors.New("invalid rerank request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeResponses:
		// Response 请求 - request 已经是正确的类型
		if responseReq, ok := request.(*dto.OpenAIResponsesRequest); ok {
			convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, *responseReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid response request type"),
				newAPIError: types.NewError(errors.New("invalid response request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	default:
		// Chat/Completion 等其他请求类型
		if generalReq, ok := request.(*dto.GeneralOpenAIRequest); ok {
			convertedRequest, err = adaptor.ConvertOpenAIRequest(c, info, generalReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid general request type"),
				newAPIError: types.NewError(errors.New("invalid general request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	}

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeConvertRequestFailed),
		}
	}
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeJsonMarshalFailed),
		}
	}
	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(requestBody)
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError),
		}
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			err := app.RelayErrorHandler(c.Request.Context(), httpResp, true)
			common.SysError(fmt.Sprintf(
				"channel test bad response: channel_id=%d name=%s type=%d model=%s endpoint_type=%s status=%d err=%v",
				channel.Id,
				channel.Name,
				channel.Type,
				testModel,
				endpointType,
				httpResp.StatusCode,
				err,
			))
			return testResult{
				context:     c,
				localErr:    err,
				newAPIError: types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError),
			}
		}
	}
	usageA, respErr := adaptor.DoResponse(c, httpResp, info)
	if respErr != nil {
		return testResult{
			context:     c,
			localErr:    respErr,
			newAPIError: respErr,
		}
	}
	if usageA == nil {
		return testResult{
			context:     c,
			localErr:    errors.New("usage is nil"),
			newAPIError: types.NewOpenAIError(errors.New("usage is nil"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
		}
	}
	usage := usageA.(*dto.Usage)
	result := w.Result()
	respBody, err := io.ReadAll(result.Body)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError),
		}
	}
	// RecordConsumeLog gate (cycle-11 plan §L3): every automatic probe used
	// to reach repo.RecordConsumeLog below unconditionally, writing a real
	// consume-log row as user 1 on every tick — polluting the leaderboard
	// and quota_data with entries that were never a real customer request.
	// The manual GET /api/channel/test/:id path (opts.RecordConsumeLog=true
	// via the testChannel wrapper) is the only caller that still wants
	// this: an operator who clicked "test" wants to see the row. Skipping
	// the whole block for the automatic pass also skips the pricing-tier
	// resettlement and quota math below, none of which anything after this
	// block reads.
	if opts.RecordConsumeLog {
		info.SetEstimatePromptTokens(usage.PromptTokens)

		// Declarative context-length pricing tier (billing-pricing-14): priceData
		// was built above (helper.ModelPriceHelper, promptTokens=0 estimate)
		// before this channel-test call had an actual response; re-evaluate
		// against the real usage before billing and writing the consume-log row
		// below, matching every other settlement site (cycle-8 plan §8 L5
		// A-F4/A-F5) — otherwise this writes a real log row from a stale tier.
		// No-op for UsePrice models and for any model with no tiers configured.
		helper.ResettleContextTier(&priceData, info.OriginModelName, usage.AsOpenAIWire().PromptTokens)

		quota := 0
		if !priceData.UsePrice {
			quota = usage.PromptTokens + int(math.Round(float64(usage.CompletionTokens)*priceData.CompletionRatio))
			quota = int(math.Round(float64(quota) * priceData.ModelRatio))
			if priceData.ModelRatio != 0 && quota <= 0 {
				quota = 1
			}
		} else {
			quota = int(priceData.ModelPrice * common.QuotaPerUnit)
		}
		tok := time.Now()
		milliseconds := tok.Sub(tik).Milliseconds()
		consumedTime := float64(milliseconds) / 1000.0
		other := app.GenerateTextOtherInfo(c, info, priceData.ModelRatio, priceData.GroupRatioInfo.GroupRatio, priceData.CompletionRatio,
			usage.PromptTokensDetails.CachedTokens, priceData.CacheRatio, priceData.ModelPrice, priceData.GroupRatioInfo.GroupSpecialRatio)
		// Attribution: c is probeChannel's own context, carrying user 1's
		// identity for the relay leg above. Re-stamp it here — after the
		// relay, before the row is written — so the row names the operator
		// who asked for the probe. resolveLogTenantID falls back to the
		// actor's own tenant when opts.TenantID is empty.
		logUserID := 1
		if opts.ActorUserID > 0 {
			logUserID = opts.ActorUserID
			c.Set("tenant_id", opts.TenantID)
			if actor, actorErr := repo.GetUserCache(logUserID); actorErr == nil {
				common.SetContextKey(c, constant.ContextKeyUserName, actor.Username)
			}
		}
		repo.RecordConsumeLog(c, logUserID, repo.RecordConsumeLogParams{
			ChannelId:        channel.Id,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			ModelName:        info.OriginModelName,
			TokenName:        "模型测试",
			Quota:            quota,
			Content:          "模型测试",
			UseTimeSeconds:   int(consumedTime),
			IsStream:         info.IsStream,
			Group:            info.UsingGroup,
			Other:            other,
		})
	}
	common.SysLog(fmt.Sprintf("testing channel #%d, response: \n%s", channel.Id, string(respBody)))
	return testResult{
		context:     c,
		localErr:    nil,
		newAPIError: nil,
	}
}

func buildTestRequest(model string, endpointType string, channel *repo.Channel) dto.Request {
	// 根据端点类型构建不同的测试请求
	if endpointType != "" {
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeEmbeddings:
			// 返回 EmbeddingRequest
			return &dto.EmbeddingRequest{
				Model: model,
				Input: []any{"hello world"},
			}
		case constant.EndpointTypeImageGeneration:
			// 返回 ImageRequest
			return &dto.ImageRequest{
				Model:  model,
				Prompt: "a cute cat",
				N:      1,
				Size:   "1024x1024",
			}
		case constant.EndpointTypeJinaRerank:
			// 返回 RerankRequest
			return &dto.RerankRequest{
				Model:     model,
				Query:     "What is Deep Learning?",
				Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
				TopN:      2,
			}
		case constant.EndpointTypeOpenAIResponse:
			// 返回 OpenAIResponsesRequest
			return &dto.OpenAIResponsesRequest{
				Model: model,
				Input: json.RawMessage("\"hi\""),
			}
		case constant.EndpointTypeAnthropic, constant.EndpointTypeGemini, constant.EndpointTypeOpenAI:
			// 返回 GeneralOpenAIRequest
			maxTokens := uint(16)
			if constant.EndpointType(endpointType) == constant.EndpointTypeGemini {
				maxTokens = 3000
			}
			return &dto.GeneralOpenAIRequest{
				Model:  model,
				Stream: false,
				Messages: []dto.Message{
					{
						Role:    "user",
						Content: "hi",
					},
				},
				MaxTokens: maxTokens,
			}
		}
	}

	// 自动检测逻辑（保持原有行为）
	// 先判断是否为 Embedding 模型
	if strings.Contains(strings.ToLower(model), "embedding") ||
		strings.HasPrefix(model, "m3e") ||
		strings.Contains(model, "bge-") {
		// 返回 EmbeddingRequest
		return &dto.EmbeddingRequest{
			Model: model,
			Input: []any{"hello world"},
		}
	}

	// Responses-only models (e.g. codex series)
	if strings.Contains(strings.ToLower(model), "codex") {
		return &dto.OpenAIResponsesRequest{
			Model: model,
			Input: json.RawMessage("\"hi\""),
		}
	}

	// Chat/Completion 请求 - 返回 GeneralOpenAIRequest
	testRequest := &dto.GeneralOpenAIRequest{
		Model:  model,
		Stream: false,
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hi",
			},
		},
	}

	if strings.HasPrefix(model, "o") {
		testRequest.MaxCompletionTokens = 16
	} else if strings.Contains(model, "thinking") {
		if !strings.Contains(model, "claude") {
			testRequest.MaxTokens = 50
		}
	} else if strings.Contains(model, "gemini") {
		testRequest.MaxTokens = 3000
	} else {
		testRequest.MaxTokens = 16
	}

	return testRequest
}

func TestChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := repo.CacheGetChannel(channelId)
	if err != nil {
		channel, err = repo.GetChannelById(channelId, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if enforceTenantScope(c, channel.TenantId) {
		return
	}
	//defer func() {
	//	if channel.ChannelInfo.IsMultiKey {
	//		go func() { _ = channel.SaveChannelInfo() }()
	//	}
	//}()
	testModel := c.Query("model")
	endpointType := c.Query("endpoint_type")
	tik := time.Now()
	result := testChannelForActor(channel, testModel, endpointType, c.GetInt("id"), c.GetString("tenant_id"))
	elapsed := time.Since(tik)
	metrics.RecordChannelProbe(string(classifyProbeResult(result)), elapsed)
	if result.localErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": result.localErr.Error(),
			"time":    0.0,
		})
		return
	}
	milliseconds := elapsed.Milliseconds()
	// Through the package's AsyncGo seam (gopool.Go in production, synchronous
	// under the test TestMain) so a test that drives this handler does not
	// leave a goroutine reading repo.DB after its cleanup swapped it.
	AsyncGo(func() { channel.UpdateResponseTime(milliseconds) })
	consumedTime := float64(milliseconds) / 1000.0
	if result.newAPIError != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": result.newAPIError.Error(),
			"time":    consumedTime,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"time":    consumedTime,
	})
}

var testAllChannelsLock sync.Mutex
var testAllChannelsRunning bool = false

func testAllChannels(notify bool) error {

	testAllChannelsLock.Lock()
	if testAllChannelsRunning {
		testAllChannelsLock.Unlock()
		return errors.New("测试已在运行中")
	}
	testAllChannelsRunning = true
	testAllChannelsLock.Unlock()
	channels, getChannelErr := repo.GetAllChannels(0, 0, true, false)
	if getChannelErr != nil {
		return getChannelErr
	}
	// L3 repair round (cycle-11 plan): threshold in time.Duration, not raw
	// milliseconds — evaluateProbeOutcome (channel_probe_policy.go) takes a
	// Duration so its tests can express thresholds like "1ms" directly. The
	// "impossible value" sentinel — 10,000,000ms (~2.8h), a latency no real
	// probe will ever exceed, so the elapsed>threshold branch never trips —
	// is unchanged from before this refactor for the operator setting the
	// threshold to exactly 0. The guard below reads `<= 0`, not `== 0` as
	// the pre-cycle-11 code did: a negative ChannelDisableThreshold used to
	// leave disableThreshold negative, and since every elapsed duration is
	// >= 0 that banned every channel on its very first pass; `<= 0` routes a
	// negative value to the same "never trips" sentinel a zero value gets,
	// which is safer but is itself a behaviour change from before this
	// cycle (undeclared in the original plan; see CONSUMER_VISIBLE_CHANGES
	// in the L3 repair-round report).
	disableThreshold := time.Duration(common.ChannelDisableThreshold * float64(time.Second))
	if disableThreshold <= 0 {
		disableThreshold = 10000000 * time.Millisecond
	}
	// NOT the package's AsyncGo seam: this launch is the point of the function
	// (testAllChannels returns as soon as the pass is LAUNCHED), and the inline
	// seam this package's TestMain installs would make it block for the whole
	// pass — turning TestChannelHealthTest_StampMeansLaunchedNotCompleted
	// (context_tasks_integration_test.go) red. L1's structural gate carries the
	// matching exemption entry (async_seam_structural_test.go); cycle-12 plan
	// L5 asked for the conversion and this is the reason it is not done here.
	gopool.Go(func() {
		// 使用 defer 确保无论如何都会重置运行状态，防止死锁
		defer func() {
			testAllChannelsLock.Lock()
			testAllChannelsRunning = false
			testAllChannelsLock.Unlock()
		}()

		for _, channel := range channels {
			autoProbeChannel(channel, disableThreshold)
			time.Sleep(common.RequestInterval)
		}

		if notify {
			app.NotifyRootUser(context.TODO(), dto.NotifyTypeChannelTest, "通道测试完成", "所有通道测试已完成")
		}
	})
	return nil
}

func TestAllChannels(c *gin.Context) {
	err := testAllChannels(true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// channelHealthTestTaskName is the "task" label this job stamps on
// metrics.LeaderTaskLastSuccess and registers under in taskreg.
//
// Leader-only (L3 repair round, cycle-11 plan): before this fix every
// master-capable replica ran its own full pass over every channel on every
// tick (taskreg.Register's leaderOnly argument below was false) — three
// replicas each independently latency-testing and ban/enable-deciding the
// same channel. The tick loop below now checks common.IsLeader() before
// doing any work, the same shape internal/lifecycle/audit_cleanup.go uses
// for its own leader-gated ticker. This is NOT NewLeaderTask, deliberately:
// that helper re-runs a full pass with ban authority on every lease
// acquisition (three times per rolling deploy), which would multiply bans
// rather than dedupe them.
//
// Stamp semantics (L3 repair round, B-F10): the heartbeat below fires when
// testAllChannels returns nil — i.e. when the async pass over every channel
// is LAUNCHED (testAllChannels hands the loop to gopool.Go and returns
// immediately), not when that pass actually finishes testing the last
// channel. Moving the stamp into the gopool.Go body would require
// testAllChannels to signal completion back to a caller that today treats
// it as fire-and-forget — a shape change to a function TestAllChannels
// (the manual "test all channels" handler) also calls, which the L3 spec's
// "no change to schedule/gating/error handling" line rules out doing as a
// side effect of a heartbeat repair. TestChannelHealthTest_StampMeansLaunchedNotCompleted
// (context_tasks_integration_test.go) proves this launch-not-completion
// shape against the real function.
//
// Active semantics (L3 repair round, B-F1): registered with a non-nil
// active func, not nil. AutoTestChannelEnabled defaults to false
// (operation_setting/monitor_setting.go), so on a default install this
// job never calls testAllChannels at all — GetSystemTasksV2 must report it
// standby ("disabled"), never overdue, in that state; a nil active func
// (meaning "always active") would make it report overdue after 2x the
// nominal interval on every default install.
const channelHealthTestTaskName = "channel-health-test"

// AutomaticallyTestChannelsWithContext tests channels with context cancellation support.
func AutomaticallyTestChannelsWithContext(ctx context.Context) {
	if !common.IsMasterNode {
		return
	}

	metrics.LeaderTaskLastSuccess.WithLabelValues(channelHealthTestTaskName).Set(0)
	taskreg.Register(channelHealthTestTaskName, func() time.Duration {
		frequency := operation_setting.GetMonitorSetting().AutoTestChannelMinutes
		return time.Duration(int(math.Round(frequency))) * time.Minute
	}, true, func() bool {
		ms := operation_setting.GetMonitorSetting()
		return ms.AutoTestChannelEnabled && ms.AutoTestChannelMinutes > 0
	})

	for {
		select {
		case <-ctx.Done():
			common.SysLog("channel auto-test stopped")
			return
		default:
		}

		if !operation_setting.GetMonitorSetting().AutoTestChannelEnabled {
			select {
			case <-ctx.Done():
				common.SysLog("channel auto-test stopped")
				return
			case <-time.After(1 * time.Minute):
				continue
			}
		}

		frequency := operation_setting.GetMonitorSetting().AutoTestChannelMinutes
		select {
		case <-ctx.Done():
			common.SysLog("channel auto-test stopped")
			return
		case <-time.After(time.Duration(int(math.Round(frequency))) * time.Minute):
			// HA: only the leader runs the probe pass; followers idle
			// (audit_cleanup.go's ticker uses the same shape). Without this
			// every master-capable replica probed every channel on every
			// tick, each with its own ban/enable authority.
			if !common.IsLeader() {
				continue
			}
			common.SysLog(fmt.Sprintf("automatically test channels with interval %f minutes", frequency))
			common.SysLog("automatically testing all channels")
			// testAllChannels takes no context: it launches its own detached
			// pass and returns immediately, so there is nothing here to
			// cancel. The heartbeat records that the pass was launched
			// (see channelHealthTestTaskName's comment). Threading ctx into
			// it is a separate change to the pass's own lifecycle.
			//nolint:contextcheck // pre-existing call shape; only the stamp below is new
			if err := testAllChannels(false); err == nil {
				metrics.RecordLeaderTaskSuccess(channelHealthTestTaskName)
			}
			common.SysLog("automatically channel test finished")
		}
	}
}
