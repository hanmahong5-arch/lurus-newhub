package bifrosteng

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

// TestDispatchChat_FakeOpenAIUpstream boots Bifrost against an httptest OpenAI-
// compatible server and asserts TextHelper-equivalent DispatchChat returns
// usage that PostConsume can settle.
func TestDispatchChat_FakeOpenAIUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" && !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "gpt-4o-mini",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello-from-fake",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     7,
				"completion_tokens": 3,
				"total_tokens":      10,
			},
		})
	}))
	t.Cleanup(srv.Close)

	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat")
	ResetConfigForTest()

	if err := Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(Shutdown)

	if !Ready() {
		t.Fatal("bifrost not ready after Init")
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4o-mini",
		StartTime:       time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         99,
			ApiType:           constant.APITypeOpenAI,
			ApiKey:            "sk-test",
			ChannelBaseUrl:    srv.URL,
			UpstreamModelName: "gpt-4o-mini",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "gpt-4o-mini",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}

	usage, apiErr := DispatchChat(c, info, req)
	if apiErr != nil {
		t.Fatalf("DispatchChat: %+v body=%s", apiErr, rec.Body.String())
	}
	if usage == nil || usage.TotalTokens != 10 {
		t.Fatalf("usage=%+v", usage)
	}
	if !strings.Contains(rec.Body.String(), "hello-from-fake") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestShouldHandleChat_LegacyOff(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "legacy")
	ResetConfigForTest()
	Shutdown()
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	if ShouldHandleChat(constant.APITypeOpenAI, req, false) {
		t.Fatal("legacy engine must not handle chat")
	}
}

func TestShouldHandleChat_UnmappedFallsThrough(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat")
	ResetConfigForTest()
	if err := Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(Shutdown)
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	if ShouldHandleChat(constant.APITypeBaidu, req, false) {
		t.Fatal("unmapped apiType must fall through to legacy")
	}
	if !ShouldHandleChat(constant.APITypeOpenAI, req, false) {
		t.Fatal("mapped openai should be handled")
	}
	if ShouldHandleChat(constant.APITypeOpenAI, req, true) {
		t.Fatal("pass-through must fall through to legacy")
	}
}
