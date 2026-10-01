package handler

// channel_test_systemone.go — the channel probe's System One legs. The probe
// (channel-test.go) defaults to a chat body, which a System One upstream
// rejects; the auto-probe would then read a healthy channel as failed and
// disable it. Every hook here is keyed on the channel type, so other channel
// types probe exactly as before.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func isSystemOneChannelType(channelType int) bool {
	return channelType == constant.ChannelTypeTypeSafe || channelType == constant.ChannelTypeSystemOneCompatible
}

// probeEndpointType pins System One channels to the systemone endpoint, even
// when the caller (the console's endpoint picker) asked for another one.
func probeEndpointType(channelType int, requested string) string {
	if isSystemOneChannelType(channelType) {
		return string(constant.EndpointTypeSystemOne)
	}
	return requested
}

// probeFallbackModel is the model probed when the channel lists none: a System
// One public name for those channels, chatDefault for every other type.
func probeFallbackModel(channelType int, chatDefault string) string {
	if isSystemOneChannelType(channelType) {
		return "jev-latest"
	}
	return chatDefault
}

// relayFormatForEndpoint maps a probe endpoint type to its relay format.
func relayFormatForEndpoint(endpointType constant.EndpointType) types.RelayFormat {
	switch endpointType {
	case constant.EndpointTypeOpenAIResponse:
		return types.RelayFormatOpenAIResponses
	case constant.EndpointTypeAnthropic:
		return types.RelayFormatClaude
	case constant.EndpointTypeGemini:
		return types.RelayFormatGemini
	case constant.EndpointTypeJinaRerank:
		return types.RelayFormatRerank
	case constant.EndpointTypeImageGeneration:
		return types.RelayFormatOpenAIImage
	case constant.EndpointTypeEmbeddings:
		return types.RelayFormatEmbedding
	case constant.EndpointTypeSystemOne:
		return types.RelayFormatSystemOne
	default:
		return types.RelayFormatOpenAI
	}
}

// buildSystemOneProbeRequest is the smallest valid System One request: one
// noul question about a one-word state.
func buildSystemOneProbeRequest(model string) *dto.SystemOneRequest {
	return &dto.SystemOneRequest{
		State: []byte(`"ping"`),
		Model: model,
		Questions: map[string]dto.SystemOneQuestion{
			"ok": {Type: "noul", Instructions: []byte(`"Is this a test message?"`)},
		},
	}
}

func convertSystemOneProbeRequest(adaptor provider.Adaptor, c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) (any, error) {
	req, ok := request.(*dto.SystemOneRequest)
	if !ok {
		return nil, errors.New("invalid systemone request type")
	}
	so, ok := adaptor.(provider.SystemOneAdaptor)
	if !ok {
		return nil, errors.New("adaptor does not serve systemone")
	}
	return so.ConvertSystemOneRequest(c, info, req)
}

// probeUpstreamModel is the model name a System One upstream is probed with:
// the channel's first model (or the fallback) through its model_mapping, the
// way the relay path would send it.
func probeUpstreamModel(channel *repo.Channel) string {
	model := probeFallbackModel(channel.Type, "")
	if models := channel.GetModels(); len(models) > 0 && strings.TrimSpace(models[0]) != "" {
		model = strings.TrimSpace(models[0])
	}
	var mapping map[string]string
	if raw := channel.GetModelMapping(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &mapping)
	}
	for hops := 0; hops <= len(mapping); hops++ {
		next, ok := mapping[model]
		if !ok || next == "" || next == model {
			break
		}
		model = next
	}
	return model
}

// testChannelV2SystemOne is TestChannelV2's probe for System One channels: the
// console's one-click test otherwise POSTs a chat completion to
// {base}/v1/chat/completions, which a System One server answers 404 for a
// perfectly healthy channel. The key is sent only when set (a self-hosted
// server may run without one, and "Bearer " with nothing after it is invalid).
func testChannelV2SystemOne(c *gin.Context, actorID int, channel *repo.Channel, baseURL string) {
	body, err := json.Marshal(buildSystemOneProbeRequest(probeUpstreamModel(channel)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to build test request"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, baseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create request: " + err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.Key != "" {
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}

	fail := func(latencyMs int64, msg string) {
		recordChannelTestAudit(c, actorID, channel.Id, false)
		c.JSON(http.StatusOK, gin.H{"success": false, "latency_ms": latencyMs, "error": msg})
	}
	tik := time.Now()
	resp, err := channelTestHTTPClient.Do(req)
	latencyMs := time.Since(tik).Milliseconds()
	if err != nil {
		fail(latencyMs, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		fail(latencyMs, fmt.Sprintf("upstream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
		return
	}
	var answer struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if json.Unmarshal(raw, &answer) != nil || len(answer.Answers) == 0 {
		fail(latencyMs, "upstream answered 200 without a System One answers object")
		return
	}
	recordChannelTestAudit(c, actorID, channel.Id, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "latency_ms": latencyMs})
}
