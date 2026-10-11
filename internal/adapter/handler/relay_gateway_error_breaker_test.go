package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// A failure the gateway produces itself says nothing about the channel's
// health, so it must never count toward the channel's breaker. The case that
// made this urgent: a chat-only adaptor answers /v1/rerank with
// provider.ErrNotImplemented -> 501, and under the default modality filter
// (observe) any API key can address a rerank call to a chat model name. 501 is
// a 5xx, so it used to be recorded as an upstream failure: CB_THRESHOLD such
// calls opened the breaker of a healthy shared channel for every tenant, and
// repeating them kept it open. The upstream is never contacted.
func TestRelay_RerankOnChatOnlyChannelDoesNotTripItsBreaker(t *testing.T) {
	pinRetryTimes(t, 0)
	reg := swapChannelBreakers(t, resilience.Config{
		Threshold:       1, // one mis-accounted failure is enough to fail the test
		Timeout:         time.Hour,
		HalfOpenTimeout: time.Hour,
	})

	var upstreamCalls atomic.Int32
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		openAIChatEchoUpstream(w, r)
	})
	// A chat-only vendor whose adaptor has no rerank endpoint.
	if err := ctx.db.Model(ctx.channel).Update("type", constant.ChannelTypeDeepSeek).Error; err != nil {
		t.Fatalf("set channel type: %v", err)
	}
	rerank := ctx.router.Group("/v1")
	rerank.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	rerank.POST("/rerank", func(c *gin.Context) { Relay(c, types.RelayFormatRerank) })

	body := `{"model":"` + ctx.channel.Models + `","query":"q","documents":["a","b"]}`
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ctx.token.Key)
		w := httptest.NewRecorder()
		ctx.router.ServeHTTP(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("rerank on a chat-only channel: status = %d, want 501; body=%s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "upstream provider") {
			t.Errorf("a gateway-side 501 was reported as the upstream's failure: %s", w.Body.String())
		}
	}
	if got := reg.GetState(ctx.channel.Id).String(); got != "closed" {
		t.Fatalf("breaker state = %q, want closed: a request the gateway refused without calling the "+
			"upstream took a healthy channel out of rotation for every tenant", got)
	}
	if n := upstreamCalls.Load(); n != 0 {
		t.Fatalf("upstream calls = %d, want 0", n)
	}

	// The channel still serves chat, immediately.
	w := ctx.postChat(t, `{"model":"`+ctx.channel.Models+`","messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("chat after rerank attempts: 503 — the channel was excluded; body=%s", w.Body.String())
	}
}
