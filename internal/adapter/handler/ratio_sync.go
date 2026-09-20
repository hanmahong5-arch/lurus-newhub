package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/logger"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const (
	defaultTimeoutSeconds = 10
	defaultEndpoint       = "/api/ratio_config"
	maxConcurrentFetches  = 8
	maxRatioConfigBytes   = 10 << 20 // 10MB
	floatEpsilon          = 1e-9
)

// ratioSyncEgressRefusedMessage is the single answer a caller gets for a
// target the egress policy refuses — whether it was refused before the dial
// (app.ValidateOutboundURL, below) or at the dial by the shared client's
// transport guard. Fixed on purpose: the per-upstream result is echoed back
// to the caller, so a message that varied with the failure ("connection
// refused" vs "no such host" vs "10.0.0.5") would turn this endpoint back
// into the internal-network port scanner the check exists to close. English
// because it travels on the API wire.
const ratioSyncEgressRefusedMessage = "upstream URL refused by egress policy"

// The two markers below are how a transport-layer refusal is recognised.
// internal/app builds both with fmt.Errorf and no wrapped sentinel, so there
// is no error value to compare against; the match is on the stable part of
// each message and ratio_sync_test.go's marker gate fails if either guard is
// reworded.
//
//   - ssrfDialGuardMarker ends both forms the dial guard produces
//     (relay_dial_guard.go: "... to internal address %s blocked by SSRF
//     guard" and "... resolves to internal address %s, blocked by SSRF
//     guard"). The pre-dial check cannot cover this one: it validates the
//     name, the guard validates what the name resolved to at connect time.
//   - the redirect pair brackets checkRedirect's "redirect to %s blocked:
//     %v" (http_client.go), whose %v is the validator message naming the
//     resolved address.
const (
	ssrfDialGuardMarker     = "blocked by SSRF guard"
	ssrfRedirectGuardPrefix = "redirect to "
	ssrfRedirectGuardMarker = " blocked: "
)

// ratioSyncUpstreamErrorMessage maps a transport error onto the text the
// caller is given. An egress refusal collapses to the one fixed message; a
// transport error that is not a refusal keeps its own text, because it
// describes a target the policy already admitted and an operator debugging
// an upstream needs to read it.
func ratioSyncUpstreamErrorMessage(err error) string {
	if isEgressRefusal(err) {
		return ratioSyncEgressRefusedMessage
	}
	return err.Error()
}

// isEgressRefusal reports whether err came from one of the two egress guards
// on the shared client (dial-time address check, redirect-hop revalidation)
// rather than from the network.
func isEgressRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, ssrfDialGuardMarker) {
		return true
	}
	return strings.Contains(msg, ssrfRedirectGuardPrefix) && strings.Contains(msg, ssrfRedirectGuardMarker)
}

func nearlyEqual(a, b float64) bool {
	if a > b {
		return a-b < floatEpsilon
	}
	return b-a < floatEpsilon
}

func valuesEqual(a, b interface{}) bool {
	af, aok := a.(float64)
	bf, bok := b.(float64)
	if aok && bok {
		return nearlyEqual(af, bf)
	}
	return a == b
}

var ratioTypes = []string{"model_ratio", "completion_ratio", "cache_ratio", "model_price"}

type upstreamResult struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data,omitempty"`
	Err  string         `json:"err,omitempty"`
}

func FetchUpstreamRatios(c *gin.Context) {
	var req dto.UpstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	if req.Timeout <= 0 {
		req.Timeout = defaultTimeoutSeconds
	}

	var upstreams []dto.UpstreamDTO

	if len(req.Upstreams) > 0 {
		for _, u := range req.Upstreams {
			if strings.HasPrefix(u.BaseURL, "http") {
				if u.Endpoint == "" {
					u.Endpoint = defaultEndpoint
				}
				u.BaseURL = strings.TrimRight(u.BaseURL, "/")
				upstreams = append(upstreams, u)
			}
		}
	} else if len(req.ChannelIDs) > 0 {
		intIds := make([]int, 0, len(req.ChannelIDs))
		for _, id64 := range req.ChannelIDs {
			intIds = append(intIds, int(id64))
		}
		dbChannels, err := repo.GetChannelsByIds(intIds)
		if err != nil {
			logger.LogError(c.Request.Context(), "failed to query channels: "+err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询渠道失败"})
			return
		}
		for _, ch := range dbChannels {
			if base := ch.GetBaseURL(); strings.HasPrefix(base, "http") {
				upstreams = append(upstreams, dto.UpstreamDTO{
					ID:       ch.Id,
					Name:     ch.Name,
					BaseURL:  strings.TrimRight(base, "/"),
					Endpoint: "",
				})
			}
		}
	}

	if len(upstreams) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无有效上游渠道"})
		return
	}

	var wg sync.WaitGroup
	ch := make(chan upstreamResult, len(upstreams))

	sem := make(chan struct{}, maxConcurrentFetches)

	// app.GetHttpClient, not a locally built transport: the shared client
	// carries the relay dial/response-header budgets the gateway's other
	// outbound calls use AND a CheckRedirect that re-runs the SSRF policy on each
	// hop — without it, a public host that answers 302 http://127.0.0.1:6379
	// walks straight around the pre-dial check below.
	client := app.GetHttpClient()

	for _, chn := range upstreams {
		wg.Add(1)
		go func(chItem dto.UpstreamDTO) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			endpoint := chItem.Endpoint
			var fullURL string
			if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
				fullURL = endpoint
			} else {
				if endpoint == "" {
					endpoint = defaultEndpoint
				} else if !strings.HasPrefix(endpoint, "/") {
					endpoint = "/" + endpoint
				}
				fullURL = chItem.BaseURL + endpoint
			}

			uniqueName := chItem.Name
			if chItem.ID != 0 {
				uniqueName = fmt.Sprintf("%s(%d)", chItem.Name, chItem.ID)
			}

			// Egress policy on the COMPOSED url, after the endpoint override
			// has been applied: an absolute `endpoint` replaces base_url
			// outright, so checking base_url alone would be walked around by
			// a public base_url plus a loopback endpoint. This is the same
			// gate channel base_url and the channel test/fetch endpoints
			// already pass (app.ValidateOutboundURL) — /api/ratio_sync/fetch
			// was the one body-supplied outbound target without it, which
			// made an admin session a reader of anything the pod can reach.
			// It runs BEFORE any dial, and answers one fixed message.
			if err := app.ValidateOutboundURL(fullURL); err != nil {
				logger.LogWarn(c.Request.Context(), "ratio_sync: refused outbound target for "+uniqueName+": "+err.Error())
				ch <- upstreamResult{Name: uniqueName, Err: ratioSyncEgressRefusedMessage}
				return
			}

			ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(req.Timeout)*time.Second)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
			if err != nil {
				logger.LogWarn(c.Request.Context(), "build request failed: "+err.Error())
				ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
				return
			}

			// 简单重试：最多 3 次，指数退避
			var resp *http.Response
			var lastErr error
			for attempt := 0; attempt < 3; attempt++ {
				resp, lastErr = client.Do(httpReq)
				if lastErr == nil {
					break
				}
				time.Sleep(time.Duration(200*(1<<attempt)) * time.Millisecond)
			}
			if lastErr != nil {
				// The log keeps the full error (an operator reading pod logs
				// is already inside the boundary); the caller gets the fixed
				// message when it was the egress policy that refused. The
				// other two error sinks in this function cannot carry a guard
				// error: one is a request-build failure, the other a decode
				// failure after a response arrived.
				logger.LogWarn(c.Request.Context(), "http error on "+chItem.Name+": "+lastErr.Error())
				ch <- upstreamResult{Name: uniqueName, Err: ratioSyncUpstreamErrorMessage(lastErr)}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				logger.LogWarn(c.Request.Context(), "non-200 from "+chItem.Name+": "+resp.Status)
				ch <- upstreamResult{Name: uniqueName, Err: resp.Status}
				return
			}

			// Content-Type 和响应体大小校验
			if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "application/json") {
				logger.LogWarn(c.Request.Context(), "unexpected content-type from "+chItem.Name+": "+ct)
			}
			limited := io.LimitReader(resp.Body, maxRatioConfigBytes)
			// 兼容两种上游接口格式：
			//  type1: /api/ratio_config -> data 为 map[string]any，包含 model_ratio/completion_ratio/cache_ratio/model_price
			//  type2: /api/pricing      -> data 为 []Pricing 列表，需要转换为与 type1 相同的 map 格式
			var body struct {
				Success bool            `json:"success"`
				Data    json.RawMessage `json:"data"`
				Message string          `json:"message"`
			}

			if err := json.NewDecoder(limited).Decode(&body); err != nil {
				logger.LogWarn(c.Request.Context(), "json decode failed from "+chItem.Name+": "+err.Error())
				ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
				return
			}

			if !body.Success {
				ch <- upstreamResult{Name: uniqueName, Err: body.Message}
				return
			}

			// 若 Data 为空，将继续按 type1 尝试解析（与多数静态 ratio_config 兼容）

			// 尝试按 type1 解析
			var type1Data map[string]any
			if err := json.Unmarshal(body.Data, &type1Data); err == nil {
				// 如果包含至少一个 ratioTypes 字段，则认为是 type1
				isType1 := false
				for _, rt := range ratioTypes {
					if _, ok := type1Data[rt]; ok {
						isType1 = true
						break
					}
				}
				if isType1 {
					ch <- upstreamResult{Name: uniqueName, Data: type1Data}
					return
				}
			}

			// 如果不是 type1，则尝试按 type2 (/api/pricing) 解析
			var pricingItems []struct {
				ModelName       string  `json:"model_name"`
				QuotaType       int     `json:"quota_type"`
				ModelRatio      float64 `json:"model_ratio"`
				ModelPrice      float64 `json:"model_price"`
				CompletionRatio float64 `json:"completion_ratio"`
			}
			if err := json.Unmarshal(body.Data, &pricingItems); err != nil {
				logger.LogWarn(c.Request.Context(), "unrecognized data format from "+chItem.Name+": "+err.Error())
				ch <- upstreamResult{Name: uniqueName, Err: "无法解析上游返回数据"}
				return
			}

			modelRatioMap := make(map[string]float64)
			completionRatioMap := make(map[string]float64)
			modelPriceMap := make(map[string]float64)

			for _, item := range pricingItems {
				if item.QuotaType == 1 {
					modelPriceMap[item.ModelName] = item.ModelPrice
				} else {
					modelRatioMap[item.ModelName] = item.ModelRatio
					// completionRatio 可能为 0，此时也直接赋值，保持与上游一致
					completionRatioMap[item.ModelName] = item.CompletionRatio
				}
			}

			converted := make(map[string]any)

			if len(modelRatioMap) > 0 {
				ratioAny := make(map[string]any, len(modelRatioMap))
				for k, v := range modelRatioMap {
					ratioAny[k] = v
				}
				converted["model_ratio"] = ratioAny
			}

			if len(completionRatioMap) > 0 {
				compAny := make(map[string]any, len(completionRatioMap))
				for k, v := range completionRatioMap {
					compAny[k] = v
				}
				converted["completion_ratio"] = compAny
			}

			if len(modelPriceMap) > 0 {
				priceAny := make(map[string]any, len(modelPriceMap))
				for k, v := range modelPriceMap {
					priceAny[k] = v
				}
				converted["model_price"] = priceAny
			}

			ch <- upstreamResult{Name: uniqueName, Data: converted}
		}(chn)
	}

	wg.Wait()
	close(ch)

	localData := ratio_setting.GetExposedData()

	var testResults []dto.TestResult
	var successfulChannels []struct {
		name string
		data map[string]any
	}

	for r := range ch {
		if r.Err != "" {
			testResults = append(testResults, dto.TestResult{
				Name:   r.Name,
				Status: "error",
				Error:  r.Err,
			})
		} else {
			testResults = append(testResults, dto.TestResult{
				Name:   r.Name,
				Status: "success",
			})
			successfulChannels = append(successfulChannels, struct {
				name string
				data map[string]any
			}{name: r.Name, data: r.Data})
		}
	}

	differences := buildDifferences(localData, successfulChannels)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"differences":  differences,
			"test_results": testResults,
		},
	})
}

func buildDifferences(localData map[string]any, successfulChannels []struct {
	name string
	data map[string]any
}) map[string]map[string]dto.DifferenceItem {
	differences := make(map[string]map[string]dto.DifferenceItem)

	allModels := make(map[string]struct{})

	for _, ratioType := range ratioTypes {
		if localRatioAny, ok := localData[ratioType]; ok {
			if localRatio, ok := localRatioAny.(map[string]float64); ok {
				for modelName := range localRatio {
					allModels[modelName] = struct{}{}
				}
			}
		}
	}

	for _, channel := range successfulChannels {
		for _, ratioType := range ratioTypes {
			if upstreamRatio, ok := channel.data[ratioType].(map[string]any); ok {
				for modelName := range upstreamRatio {
					allModels[modelName] = struct{}{}
				}
			}
		}
	}

	confidenceMap := make(map[string]map[string]bool)

	// 预处理阶段：检查pricing接口的可信度
	for _, channel := range successfulChannels {
		confidenceMap[channel.name] = make(map[string]bool)

		modelRatios, hasModelRatio := channel.data["model_ratio"].(map[string]any)
		completionRatios, hasCompletionRatio := channel.data["completion_ratio"].(map[string]any)

		if hasModelRatio && hasCompletionRatio {
			// 遍历所有模型，检查是否满足不可信条件
			for modelName := range allModels {
				// 默认为可信
				confidenceMap[channel.name][modelName] = true

				// 检查是否满足不可信条件：model_ratio为37.5且completion_ratio为1
				if modelRatioVal, ok := modelRatios[modelName]; ok {
					if completionRatioVal, ok := completionRatios[modelName]; ok {
						// 转换为float64进行比较
						if modelRatioFloat, ok := modelRatioVal.(float64); ok {
							if completionRatioFloat, ok := completionRatioVal.(float64); ok {
								if modelRatioFloat == 37.5 && completionRatioFloat == 1.0 {
									confidenceMap[channel.name][modelName] = false
								}
							}
						}
					}
				}
			}
		} else {
			// 如果不是从pricing接口获取的数据，则全部标记为可信
			for modelName := range allModels {
				confidenceMap[channel.name][modelName] = true
			}
		}
	}

	for modelName := range allModels {
		for _, ratioType := range ratioTypes {
			var localValue interface{} = nil
			if localRatioAny, ok := localData[ratioType]; ok {
				if localRatio, ok := localRatioAny.(map[string]float64); ok {
					if val, exists := localRatio[modelName]; exists {
						localValue = val
					}
				}
			}

			upstreamValues := make(map[string]interface{})
			confidenceValues := make(map[string]bool)
			hasUpstreamValue := false
			hasDifference := false

			for _, channel := range successfulChannels {
				var upstreamValue interface{} = nil

				if upstreamRatio, ok := channel.data[ratioType].(map[string]any); ok {
					if val, exists := upstreamRatio[modelName]; exists {
						upstreamValue = val
						hasUpstreamValue = true

						if localValue != nil && !valuesEqual(localValue, val) {
							hasDifference = true
						} else if valuesEqual(localValue, val) {
							upstreamValue = "same"
						}
					}
				}
				if upstreamValue == nil && localValue == nil {
					upstreamValue = "same"
				}

				if localValue == nil && upstreamValue != nil && upstreamValue != "same" {
					hasDifference = true
				}

				upstreamValues[channel.name] = upstreamValue

				confidenceValues[channel.name] = confidenceMap[channel.name][modelName]
			}

			shouldInclude := false

			if localValue != nil {
				if hasDifference {
					shouldInclude = true
				}
			} else {
				if hasUpstreamValue {
					shouldInclude = true
				}
			}

			if shouldInclude {
				if differences[modelName] == nil {
					differences[modelName] = make(map[string]dto.DifferenceItem)
				}
				differences[modelName][ratioType] = dto.DifferenceItem{
					Current:    localValue,
					Upstreams:  upstreamValues,
					Confidence: confidenceValues,
				}
			}
		}
	}

	channelHasDiff := make(map[string]bool)
	for _, ratioMap := range differences {
		for _, item := range ratioMap {
			for chName, val := range item.Upstreams {
				if val != nil && val != "same" {
					channelHasDiff[chName] = true
				}
			}
		}
	}

	for modelName, ratioMap := range differences {
		for ratioType, item := range ratioMap {
			for chName := range item.Upstreams {
				if !channelHasDiff[chName] {
					delete(item.Upstreams, chName)
					delete(item.Confidence, chName)
				}
			}

			allSame := true
			for _, v := range item.Upstreams {
				if v != "same" {
					allSame = false
					break
				}
			}
			if len(item.Upstreams) == 0 || allSame {
				delete(ratioMap, ratioType)
			} else {
				differences[modelName][ratioType] = item
			}
		}

		if len(ratioMap) == 0 {
			delete(differences, modelName)
		}
	}

	return differences
}

func GetSyncableChannels(c *gin.Context) {
	channels, err := repo.GetAllChannels(0, 0, true, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	var syncableChannels []dto.SyncableChannel
	for _, channel := range channels {
		if channel.GetBaseURL() != "" {
			syncableChannels = append(syncableChannels, dto.SyncableChannel{
				ID:      channel.Id,
				Name:    channel.Name,
				BaseURL: channel.GetBaseURL(),
				Status:  channel.Status,
			})
		}
	}

	syncableChannels = append(syncableChannels, dto.SyncableChannel{
		ID:      -100,
		Name:    "官方倍率预设",
		BaseURL: "https://basellm.github.io",
		Status:  1,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    syncableChannels,
	})
}
