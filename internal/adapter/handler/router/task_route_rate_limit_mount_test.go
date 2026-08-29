package router

// task_route_rate_limit_mount_test.go — mount locks for the task-relay groups
// (/mj, /suno, /v1/audio/music). These groups historically mounted only
// TokenAuth+PoolBalanceCheck(+Distribute), so ModelRequestRateLimit — and the
// whole enforcement chain — never applied to them at all: a token pinned to 1
// request/min on /v1 could still hammer /mj/submit/imagine without limit.
// Same two-request 429 pattern (and the same seeded-token fixture) as
// r6a_rate_limit_mount_test.go; see that file for why the fixture works the
// way it does.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
)

// taskRouteRateLimitHarness applies the 1-request window used by every test in
// this file and returns a router built by the real SetRelayRouter.
func taskRouteRateLimitHarness(t *testing.T) (engine *gin.Engine, authHeader string) {
	t.Helper()

	key, cleanup := r6aSeedRateLimitToken(t)
	t.Cleanup(cleanup)

	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 1
	setting.ModelRequestRateLimitSuccessCount = 0
	setting.ModelRequestRateLimitDurationMinutes = 1
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
	})

	gin.SetMode(gin.TestMode)
	engine = gin.New()
	SetRelayRouter(engine)
	return engine, "Bearer sk-" + key
}

// fireTwice sends the same authenticated request twice and asserts the second
// one is a 429 from ModelRequestRateLimit specifically (X-RateLimit-Limit
// fingerprint — see r6a_rate_limit_mount_test.go:174-184 for why a bare
// status-code check could false-pass).
func fireTwice(t *testing.T, engine *gin.Engine, authHeader, method, path string) {
	t.Helper()

	req1 := httptest.NewRequest(method, path, nil)
	req1.Header.Set("Authorization", authHeader)
	w1 := httptest.NewRecorder()
	engine.ServeHTTP(w1, req1)
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("%s %s: first request already got 429 — fixture broken; body=%s", method, path, w1.Body.String())
	}

	req2 := httptest.NewRequest(method, path, nil)
	req2.Header.Set("Authorization", authHeader)
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("%s %s: second request status=%d, want 429 (ModelRequestRateLimit must be mounted on this task-relay chain); body=%s", method, path, w2.Code, w2.Body.String())
	}
	if got := w2.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("%s %s: second request X-RateLimit-Limit=%q, want \"1\" — a 429 from a different limiter would false-pass a bare status check", method, path, got)
	}
}

// TestSetRelayRouter_ModelRequestRateLimit_MountedOnMjChain locks the /mj
// group (and, through registerMjRouterGroup, the /:mode/mj twin).
func TestSetRelayRouter_ModelRequestRateLimit_MountedOnMjChain(t *testing.T) {
	engine, auth := taskRouteRateLimitHarness(t)
	fireTwice(t, engine, auth, http.MethodPost, "/mj/submit/imagine")
}

// TestSetRelayRouter_ModelRequestRateLimit_MountedOnSunoChain locks /suno.
func TestSetRelayRouter_ModelRequestRateLimit_MountedOnSunoChain(t *testing.T) {
	engine, auth := taskRouteRateLimitHarness(t)
	fireTwice(t, engine, auth, http.MethodPost, "/suno/submit/music")
}

// TestSetRelayRouter_ModelRequestRateLimit_MountedOnMusicChain locks the
// /v1/audio/music sibling group — it is NOT a child of relayV1Router, so the
// /v1 chain's mount (r6a test) says nothing about it.
func TestSetRelayRouter_ModelRequestRateLimit_MountedOnMusicChain(t *testing.T) {
	engine, auth := taskRouteRateLimitHarness(t)
	fireTwice(t, engine, auth, http.MethodPost, "/v1/audio/music")
}

// TestSetRelayRouter_MjImageProxy_NotRateLimited locks the deliberate
// exception: the /mj/image/:id proxy is registered BEFORE the enforcement
// .Use, because gallery UIs burst-load images and counting thumbnail GETs
// against the request window would starve real submits. If a refactor moves
// the registration below the enforcement chain, this turns red.
func TestSetRelayRouter_MjImageProxy_NotRateLimited(t *testing.T) {
	engine, auth := taskRouteRateLimitHarness(t)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/mj/image/task-123", nil)
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("image proxy request %d got 429 — the proxy must stay OUTSIDE the rate-limit chain (see registerMjRouterGroup)", i+1)
		}
	}
}
