package router

// r11_v2_rate_limit_mount_test.go — cycle-11 L5/W: proves
// middleware.GlobalV2RateLimit() is actually reached through the real /api/v2
// route table (SetApiV2Router), not merely exercised directly on a bare
// gin.New() the way rate_limit_v2_test.go does (per this cycle's REAL-CHAIN
// RULE, that is not proof of mounting).
//
// Mutation: commenting out `apiV2.Use(middleware.GlobalV2RateLimit())` in
// api-v2-router.go makes the 4th call below stop 429ing.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func TestSetApiV2Router_GlobalV2RateLimit_MountedAndRejects(t *testing.T) {
	prevRedis := common.RedisEnabled
	prevEnable := common.GlobalV2RateLimitEnable
	prevNum := common.GlobalV2RateLimitNum
	prevDur := common.GlobalV2RateLimitDuration
	common.RedisEnabled = false
	common.GlobalV2RateLimitEnable = true
	common.GlobalV2RateLimitNum = 3
	common.GlobalV2RateLimitDuration = 180
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.GlobalV2RateLimitEnable = prevEnable
		common.GlobalV2RateLimitNum = prevNum
		common.GlobalV2RateLimitDuration = prevDur
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	// GET /api/v2/relays/recommended: public, read-only, no DB touch
	// (handler.GetRecommendedRelays reads only the in-memory OptionMap) —
	// harmless to call repeatedly.
	const path = "/api/v2/relays/recommended"
	const remoteAddr = "203.0.113.50:5555"
	forwarded := map[string]string{"X-Forwarded-For": "203.0.113.50"}

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = remoteAddr
		for k, v := range forwarded {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	for i := 1; i <= 3; i++ {
		w := do()
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("call %d = 429, want <3rd call admitted (budget is 3); body=%s", i, w.Body.String())
		}
	}

	w4 := do()
	if w4.Code != http.StatusTooManyRequests {
		t.Fatalf("4th call = %d, want 429 (GlobalV2RateLimit must be mounted on the real /api/v2 chain and enforce its budget); body=%s", w4.Code, w4.Body.String())
	}
	if got := w4.Header().Get("X-RateLimit-Limit"); got != "3" {
		t.Fatalf("4th call X-RateLimit-Limit=%q, want \"3\"", got)
	}
}
