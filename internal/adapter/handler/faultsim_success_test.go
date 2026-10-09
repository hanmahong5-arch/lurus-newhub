package handler

// faultsim_success_test.go — oracle for the faultsim success mode (model
// "ok" / "ok-*"). The interesting claim is not that the bytes look right but
// that newhub's OWN relay parses them: each wire is driven through the real
// middleware chain (TokenAuth -> Distribute -> Relay) against the simulator
// and the settled consume-log row must carry exactly 1000 / 500 tokens.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

const faultSimTestKey = "sk-upstream-dummy"

// faultSimUpstream is the simulator exactly as the router mounts it, under the
// paths the channel adaptors append to a base URL.
func faultSimUpstream() http.HandlerFunc {
	e := gin.New()
	e.POST("/v1/chat/completions", FaultSimChatCompletions)
	e.POST("/v1/responses", FaultSimResponses)
	e.POST("/v1/messages", FaultSimMessages)
	return e.ServeHTTP
}

func faultSimDoPost(h http.Handler, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestFaultSimOK_WireShapes(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "tok")
	gin.SetMode(gin.TestMode)
	h := faultSimUpstream()
	auth := map[string]string{"X-Faultsim-Token": "tok"}

	t.Run("chat non-stream", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"ok","messages":[]}`, auth)
		b := w.Body.String()
		if w.Code != 200 || !strings.Contains(b, `"prompt_tokens":1000`) || !strings.Contains(b, `"completion_tokens":500`) || !strings.Contains(b, "固定回复") {
			t.Fatalf("code=%d body=%s", w.Code, b)
		}
	})
	t.Run("chat stream ends with usage then DONE, without asking for include_usage", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"ok-chat","stream":true}`, auth)
		b := w.Body.String()
		ui := strings.Index(b, `"usage"`)
		di := strings.Index(b, "data: [DONE]")
		if ui < 0 || di < ui || !strings.Contains(b, `"prompt_tokens":1000`) {
			t.Fatalf("usage must precede [DONE]:\n%s", b)
		}
	})
	t.Run("usage header overrides", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"ok-x"}`, map[string]string{"X-Faultsim-Token": "tok", FaultSimUsageHeader: "7, 9"})
		if !strings.Contains(w.Body.String(), `"prompt_tokens":7`) || !strings.Contains(w.Body.String(), `"completion_tokens":9`) {
			t.Fatalf("body=%s", w.Body.String())
		}
	})
	t.Run("bad usage header is 400", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"ok"}`, map[string]string{"X-Faultsim-Token": "tok", FaultSimUsageHeader: "abc"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("responses and anthropic", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/responses", `{"model":"ok"}`, auth)
		if !strings.Contains(w.Body.String(), `"input_tokens":1000`) {
			t.Fatalf("responses body=%s", w.Body.String())
		}
		w = faultSimDoPost(h, "/v1/messages", `{"model":"ok","stream":true}`, map[string]string{"x-api-key": "tok"})
		b := w.Body.String()
		for _, ev := range []string{"message_start", "content_block_delta", "message_delta", "message_stop"} {
			if !strings.Contains(b, "event: "+ev) {
				t.Fatalf("missing %s:\n%s", ev, b)
			}
		}
	})
	t.Run("ok prefix is exact: okay is not success", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"okay"}`, auth)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("success mode still needs the token", func(t *testing.T) {
		w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"ok"}`, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("fault modes unchanged", func(t *testing.T) {
		if w := faultSimDoPost(h, "/v1/chat/completions", `{"model":"http_500"}`, auth); w.Code != http.StatusInternalServerError {
			t.Fatalf("code=%d", w.Code)
		}
		if w := faultSimDoPost(h, "/v1/messages", `{"model":"rate_limit_429"}`, auth); w.Code != http.StatusTooManyRequests {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// TestFaultSimOK_RelayParsesUsageOnEveryWire is the end-to-end oracle.
func TestFaultSimOK_RelayParsesUsageOnEveryWire(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", faultSimTestKey)
	ctx := setupRelaySuccessRouter(t, faultSimUpstream())

	// The stream scanner's idle timer needs a positive value.
	prevTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	t.Cleanup(func() { constant.StreamingTimeout = prevTimeout })

	// An unpriced model is refused before it reaches the channel; the UAT seed
	// prices the ok-* models for the same reason.
	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prevRatio) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"ok-chat":1.0,"ok-msgs":1.0}`); err != nil {
		t.Fatal(err)
	}

	// The OpenAI channel serves chat + responses; add an Anthropic channel.
	ctx.channel.Models = "ok-chat"
	if err := ctx.db.Save(ctx.channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := ctx.db.Where("channel_id = ?", ctx.channel.Id).Delete(&repo.Ability{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ctx.channel.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	base := ctx.upstream.URL
	weight, prio := uint(10), int64(0)
	msgCh := &repo.Channel{
		Id: ctx.channel.Id + 1, TenantId: "default", Type: constant.ChannelTypeAnthropic, Key: faultSimTestKey,
		Status: common.ChannelStatusEnabled, Name: "faultsim-messages", BaseURL: &base,
		Models: "ok-msgs", Group: "default", Weight: &weight, Priority: &prio,
	}
	if err := ctx.db.Create(msgCh).Error; err != nil {
		t.Fatal(err)
	}
	if err := msgCh.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	g := ctx.router.Group("/v1")
	g.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	g.POST("/responses", func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
	g.POST("/messages", func(c *gin.Context) { Relay(c, types.RelayFormatClaude) })

	cases := []struct{ name, path, body string }{
		{"chat", "/v1/chat/completions", `{"model":"ok-chat","messages":[{"role":"user","content":"hi"}]}`},
		{"chat stream", "/v1/chat/completions", `{"model":"ok-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`},
		{"responses", "/v1/responses", `{"model":"ok-chat","input":"hi"}`},
		{"responses stream", "/v1/responses", `{"model":"ok-chat","input":"hi","stream":true}`},
		{"anthropic", "/v1/messages", `{"model":"ok-msgs","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`},
		{"anthropic stream", "/v1/messages", `{"model":"ok-msgs","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+ctx.token.Key)
			req.Header.Set("anthropic-version", "2023-06-01")
			w := httptest.NewRecorder()
			ctx.router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var logs []repo.Log
			if err := ctx.db.Where("type = ?", repo.LogTypeConsume).Order("id").Find(&logs).Error; err != nil {
				t.Fatal(err)
			}
			if len(logs) != i+1 {
				t.Fatalf("consume rows = %d, want %d; resp=%s", len(logs), i+1, w.Body.String())
			}
			last := logs[len(logs)-1]
			if last.PromptTokens != 1000 || last.CompletionTokens != 500 {
				t.Fatalf("%s: settled usage %d/%d, want 1000/500", tc.name, last.PromptTokens, last.CompletionTokens)
			}
		})
	}
}
