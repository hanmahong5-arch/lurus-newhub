package router

// r11_probe_bypass_mount_test.go — cycle-11 L2/W: proves the k8s probe
// bypass in middleware.GlobalAPIRateLimit (rate-limit.go's probeBypassPaths
// branch) is actually reached through the real /api route table
// (SetApiRouter), not just exercised on a hand-built engine like
// internal/adapter/middleware/api_rate_limit_probe_bypass_test.go does.
// Covers both probe paths: /api/health (readinessProbe) and /api/status
// (livenessProbe — the one whose failure restarts the pod).
//
// Mutation: deleting the probe-bypass branch in
// internal/adapter/middleware/rate-limit.go makes the direct-case assertions
// below fail (3rd direct call would 429 instead of 200).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func TestSetApiRouter_ProbeBypass_MountedOnRealRouteTable(t *testing.T) {
	prevRedis := common.RedisEnabled
	prevEnable := common.GlobalApiRateLimitEnable
	prevNum := common.GlobalApiRateLimitNum
	prevDur := common.GlobalApiRateLimitDuration
	common.RedisEnabled = false
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = 1
	common.GlobalApiRateLimitDuration = 180
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.GlobalApiRateLimitEnable = prevEnable
		common.GlobalApiRateLimitNum = prevNum
		common.GlobalApiRateLimitDuration = prevDur
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	do := func(remoteAddr, path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = remoteAddr
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// Direct in-cluster, no forwarding header: bypass applies, a budget of 1
	// would normally 429 the 2nd/3rd call but all three must succeed — for
	// BOTH probe paths, driven through the real mounted router.
	for i := 1; i <= 3; i++ {
		w := do("10.20.30.1:5555", "/api/health", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("direct /api/health call %d through SetApiRouter = %d, want 200 (bypass must not throttle probes); body=%s", i, w.Code, w.Body.String())
		}
	}
	for i := 1; i <= 3; i++ {
		w := do("10.20.30.4:5555", "/api/status", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("direct /api/status call %d through SetApiRouter = %d, want 200 (bypass must not throttle probes); body=%s", i, w.Code, w.Body.String())
		}
	}

	// Forwarded case (nginx-relayed): not direct in-cluster, stays rate
	// limited — 2nd call with a budget of 1 must 429. For both probe paths.
	healthForwarded := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	if w := do("10.20.30.2:5555", "/api/health", healthForwarded); w.Code != http.StatusOK {
		t.Fatalf("forwarded /api/health 1st call = %d, want 200", w.Code)
	}
	if w := do("10.20.30.2:5555", "/api/health", healthForwarded); w.Code != http.StatusTooManyRequests {
		t.Fatalf("forwarded /api/health 2nd call = %d, want 429 (a forwarded probe is not exempt)", w.Code)
	}

	statusForwarded := map[string]string{"X-Forwarded-For": "203.0.113.10"}
	if w := do("10.20.30.3:5555", "/api/status", statusForwarded); w.Code != http.StatusOK {
		t.Fatalf("forwarded /api/status 1st call = %d, want 200", w.Code)
	}
	if w := do("10.20.30.3:5555", "/api/status", statusForwarded); w.Code != http.StatusTooManyRequests {
		t.Fatalf("forwarded /api/status 2nd call = %d, want 429 (a forwarded probe is not exempt)", w.Code)
	}
}
