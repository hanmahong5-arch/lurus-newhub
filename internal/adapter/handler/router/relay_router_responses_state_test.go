package router

// relay_router_responses_state_test.go — mount lock for the stateful
// GET/DELETE /v1/responses/:response_id group (cycle-8 L7,
// tasks-plugins-12). Proves, through the REAL router
// (SetRelayRouter — the same function cmd/server/main.go reaches via
// SetRouter), that this group does NOT run middleware.Distribute(): a
// request against an absent response_id must answer the handler's own 404
// response_not_found, never Distribute's "Invalid request, ..." 400 that
// would fire first if Distribute() were mistakenly mounted on this chain
// (GET/DELETE carry no body, so Distribute's getModelRequest(c) body-parse
// fails immediately on this group if present — see distributor.go:69-73).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

func TestRelayRouter_ResponsesStateGroup_NoDistribute(t *testing.T) {
	key, cleanup := r6aSeedRateLimitToken(t)
	t.Cleanup(cleanup)
	if err := repo.DB.AutoMigrate(&entity.ResponseRegistry{}); err != nil {
		t.Fatalf("auto migrate ResponseRegistry: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_absent_registry_probe", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("got 400 (Distribute's body-parse rejection) — Distribute() must NOT be mounted on the responses-state group; body=%s", w.Body.String())
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (response_not_found from the handler); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "response_not_found") {
		t.Errorf("body = %s, want it to contain the response_not_found error code", w.Body.String())
	}
}

// TestRelayRouter_ResponsesStateGroup_RequiresTokenAuth locks the other half
// of the chain: an unauthenticated request must be rejected by TokenAuth,
// not reach the handler at all (which would otherwise 404 the same way,
// masking a missing-auth regression as "route works").
func TestRelayRouter_ResponsesStateGroup_RequiresTokenAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_no_auth", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("got 404 with no Authorization header — TokenAuth must reject before the handler runs; body=%s", w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (TokenAuth rejection); body=%s", w.Code, w.Body.String())
	}
}

// TestRelayRouter_ResponsesStateGroup_OwnRateLimitBucket is the oracle for
// cycle-8 L7 repair round finding B-F1: the group must reject with its OWN
// "RS" bucket (30 req/60s per IP), not CriticalRateLimit's shared "CT"
// bucket (20 req/20min, shared with TOTP-disable/channel-key-reveal/audit
// -export on /api/*), and the reject must carry the OpenAI-wire error
// envelope with a code — not an empty body. Drives the REAL router
// (SetRelayRouter) with distinct client IPs per sub-test so the
// package-level in-memory limiter singleton can't leak state across runs.
func TestRelayRouter_ResponsesStateGroup_OwnRateLimitBucket(t *testing.T) {
	key, cleanup := r6aSeedRateLimitToken(t)
	t.Cleanup(cleanup)
	if err := repo.DB.AutoMigrate(&entity.ResponseRegistry{}); err != nil {
		t.Fatalf("auto migrate ResponseRegistry: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	// A budget of 20 (CriticalRateLimit's own cap) must NOT trip this
	// group's limiter — proving it is not sharing CriticalRateLimit's "CT"
	// bucket. If this ever regresses back to CriticalRateLimit, request 21
	// would already be a 429.
	const underCTBudget = 20
	clientIP := "203.0.113.77"
	var lastCode int
	var lastBody string
	for i := 0; i < underCTBudget; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_probe_bucket", nil)
		req.Header.Set("Authorization", "Bearer sk-"+key)
		req.RemoteAddr = clientIP + ":1234"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		lastCode, lastBody = w.Code, w.Body.String()
	}
	if lastCode == http.StatusTooManyRequests {
		t.Fatalf("request %d already got 429 at a budget (%d) below CriticalRateLimit's own 20-request cap — this group must NOT share CriticalRateLimit's \"CT\" bucket; body=%s", underCTBudget, underCTBudget, lastBody)
	}

	// Now exceed this group's OWN budget (30/60s) — requests 21..31 on the
	// SAME client IP — and assert the 429 carries a wire body with a code,
	// not an empty CriticalRateLimit-style reject.
	var tripped *httptest.ResponseRecorder
	for i := underCTBudget; i < 35; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_probe_bucket", nil)
		req.Header.Set("Authorization", "Bearer sk-"+key)
		req.RemoteAddr = clientIP + ":1234"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			tripped = w
			break
		}
	}
	if tripped == nil {
		t.Fatal("never got a 429 within 35 requests from one client IP — ResponsesStateRateLimit does not appear to be enforcing at all")
	}
	if !strings.Contains(tripped.Body.String(), "request_rate_limit_exceeded") {
		t.Errorf("429 body = %q, want it to carry error.code=request_rate_limit_exceeded (the OpenAI-wire envelope, not an empty CriticalRateLimit-style reject)", tripped.Body.String())
	}
	if tripped.Header().Get("X-RateLimit-Scope") != "ip" {
		t.Errorf("X-RateLimit-Scope = %q, want %q", tripped.Header().Get("X-RateLimit-Scope"), "ip")
	}
	if tripped.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on the 429")
	}
}
