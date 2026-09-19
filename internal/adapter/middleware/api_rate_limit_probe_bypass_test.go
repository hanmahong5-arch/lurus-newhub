package middleware

// api_rate_limit_probe_bypass_test.go — cycle-11 L2: k8s probe paths
// (/api/status, /api/health) bypass GlobalAPIRateLimit when the request is
// a direct in-cluster hit (loopback/private RemoteAddr, no forwarding
// header — in today's topology that is the kubelet on the pod's NodePort),
// but a request relayed through the host nginx — which sets a forwarding
// header on what it relays (deploy/r6-host-nginx/*.conf) — stays rate
// limited like any other /api route. Drives the real GlobalAPIRateLimit()
// middleware mounted on a real gin engine, not a hand-built stand-in.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func TestGlobalAPIRateLimit_DirectProbePathsBypassLimiter(t *testing.T) {
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
	r := gin.New()
	r.GET("/api/health", GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/status", GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/other", GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(remoteAddr, path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = remoteAddr
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Direct in-cluster: private RemoteAddr, no forwarding header. A budget
	// of 1 would normally 429 the 2nd and 3rd calls, but /api/health is
	// bypassed entirely, so all three succeed.
	for i := 1; i <= 3; i++ {
		w := do("10.20.30.1:5555", "/api/health", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("direct /api/health call %d = %d, want 200 (bypass must not throttle probes)", i, w.Code)
		}
	}

	// Same shape for /api/status — the livenessProbe path (whose failure is
	// the one that restarts the pod, not just fails readiness).
	for i := 1; i <= 3; i++ {
		w := do("10.20.30.1:5555", "/api/status", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("direct /api/status call %d = %d, want 200 (bypass must not throttle probes)", i, w.Code)
		}
	}

	// /api/status relayed through nginx: not direct, stays rate limited.
	statusForwarded := map[string]string{"X-Forwarded-For": "203.0.113.10"}
	if w := do("10.20.30.3:5555", "/api/status", statusForwarded); w.Code != http.StatusOK {
		t.Fatalf("forwarded /api/status 1st call = %d, want 200", w.Code)
	}
	if w := do("10.20.30.3:5555", "/api/status", statusForwarded); w.Code != http.StatusTooManyRequests {
		t.Fatalf("forwarded /api/status 2nd call = %d, want 429 (a forwarded probe is not exempt)", w.Code)
	}

	// Same direct-in-cluster caller, non-probe path: normal limiter applies.
	if w := do("10.20.30.1:5555", "/api/other", nil); w.Code != http.StatusOK {
		t.Fatalf("direct /api/other 1st call = %d, want 200", w.Code)
	}
	if w := do("10.20.30.1:5555", "/api/other", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("direct /api/other 2nd call = %d, want 429 (probe bypass must not leak to non-probe paths)", w.Code)
	}

	// /api/health relayed through nginx (forwarding header present): no
	// longer looks direct in-cluster, so the limiter applies like any other
	// route — 2nd call with a budget of 1 must 429.
	forwarded := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	if w := do("10.20.30.2:5555", "/api/health", forwarded); w.Code != http.StatusOK {
		t.Fatalf("forwarded /api/health 1st call = %d, want 200", w.Code)
	}
	if w := do("10.20.30.2:5555", "/api/health", forwarded); w.Code != http.StatusTooManyRequests {
		t.Fatalf("forwarded /api/health 2nd call = %d, want 429 (a forwarded probe is not exempt)", w.Code)
	}
}
