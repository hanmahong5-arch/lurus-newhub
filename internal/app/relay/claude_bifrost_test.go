package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/bifrosteng"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func TestTryBifrostClaude_LegacyFallsThrough(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "legacy")
	bifrosteng.ResetConfigForTest()
	bifrosteng.Shutdown()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeAnthropic},
	}
	handled, err := tryBifrostClaude(c, info, &dto.ClaudeRequest{
		Model:    "claude-3-5-sonnet",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	})
	if handled {
		t.Fatal("legacy engine must fall through")
	}
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestTryBifrostClaude_PassThroughFallsThrough(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat,messages")
	bifrosteng.ResetConfigForTest()
	bifrosteng.Shutdown()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeAnthropic},
	}
	info.ChannelSetting = dto.ChannelSettings{PassThroughBodyEnabled: true}
	handled, err := tryBifrostClaude(c, info, &dto.ClaudeRequest{Model: "claude-3-5-sonnet"})
	if handled {
		t.Fatal("pass-through must fall through even when messages mode is on")
	}
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestTryBifrostClaude_UpstreamErrorIsHandled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat,messages")
	bifrosteng.ResetConfigForTest()
	if err := bifrosteng.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(bifrosteng.Shutdown)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         77,
			ApiType:           constant.APITypeAnthropic,
			ApiKey:            "sk-test",
			ChannelBaseUrl:    srv.URL,
			UpstreamModelName: "claude-3-5-sonnet",
		},
	}
	req := &dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 64,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	handled, apiErr := tryBifrostClaude(c, info, req)
	if !handled {
		t.Fatal("ready bifrost + convertible Claude request must be handled")
	}
	if apiErr == nil {
		t.Fatal("upstream 502 must surface as apiErr (and must not settle)")
	}
}

func TestTextResponseToOpenAI(t *testing.T) {
	if got := textResponseToOpenAI(nil); got == nil || got.Id != "" {
		t.Fatalf("nil → empty OpenAI response, got %#v", got)
	}
	in := &dto.TextResponse{
		Id:      "resp-1",
		Object:  "chat.completion",
		Created: 42,
		Model:   "claude-3-5-sonnet",
		Choices: []dto.OpenAITextResponseChoice{{Index: 0, FinishReason: "stop"}},
		Usage:   dto.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	}
	out := textResponseToOpenAI(in)
	if out.Id != "resp-1" || out.Model != "claude-3-5-sonnet" || out.TotalTokens != 3 {
		t.Fatalf("round-trip mismatch: %#v", out)
	}
	if len(out.Choices) != 1 || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices: %#v", out.Choices)
	}
}
