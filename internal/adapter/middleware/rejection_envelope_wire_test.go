package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// mountWireRejectionRouter mirrors relay-router.go's group layout for the
// three wire shapes this suite probes: StampRelayFormat is the first Use()
// on each group, ahead of TokenAuth (and, when passed, PoolBalanceCheck) —
// exactly the ordering relay-router.go now mounts, and load-bearing because
// gin snapshots a group's chain at Use()/Group() time.
func mountWireRejectionRouter(extra ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	terminal := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) }

	v1 := r.Group("/v1")
	v1.Use(append([]gin.HandlerFunc{StampRelayFormat(), TokenAuth()}, extra...)...)
	v1.POST("/messages", terminal)
	v1.POST("/chat/completions", terminal)

	v1beta := r.Group("/v1beta")
	v1beta.Use(append([]gin.HandlerFunc{StampRelayFormat(), TokenAuth()}, extra...)...)
	v1beta.POST("/models/*path", terminal)

	return r
}

// TestMiddlewareRejection_EnvelopeIsWireNative pins the defect this lane
// closes: a middleware-stage rejection (here, TokenAuth's bad-key 401) must
// answer in the caller's own wire shape, not always OpenAI's — a Claude or
// Gemini SDK cannot parse an OpenAI envelope and surfaces a decode failure
// that hides the real 401 reason.
func TestMiddlewareRejection_EnvelopeIsWireNative(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountWireRejectionRouter()

	// Claude wire: /v1/messages with an unrecognised key.
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer nosuchtokenatall")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/messages bad key: status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, `"error":{`) {
		t.Errorf(`/v1/messages 401 body = %s, want a Claude envelope ("type":"error","error":{...})`, body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("/v1/messages 401 body = %s, must not leak the OpenAI-wire error type onto the Claude wire", body)
	}

	// Gemini wire: /v1beta/models/<model>:generateContent with an
	// unrecognised key (Gemini callers authenticate via x-goog-api-key).
	req2 := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", strings.NewReader(`{}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("x-goog-api-key", "nosuchtokenatall")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("/v1beta bad key: status = %d, want 401; body=%s", w2.Code, w2.Body.String())
	}
	body2 := w2.Body.String()
	if !strings.HasPrefix(body2, `{"error":{"code":401`) {
		t.Errorf(`/v1beta 401 body = %s, want a Gemini envelope starting {"error":{"code":401`, body2)
	}
	if !strings.Contains(body2, `"status":"UNAUTHENTICATED"`) {
		t.Errorf(`/v1beta 401 body = %s, want "status":"UNAUTHENTICATED"`, body2)
	}

	// OpenAI wire, unchanged: /v1/chat/completions with the same bad key
	// must still answer the pre-existing new_api_error shape.
	req3 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer nosuchtokenatall")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/chat/completions bad key: status = %d, want 401; body=%s", w3.Code, w3.Body.String())
	}
	if !strings.Contains(w3.Body.String(), `"type":"new_api_error"`) {
		t.Errorf(`/v1/chat/completions 401 body = %s, want the untouched OpenAI wire (type=new_api_error)`, w3.Body.String())
	}
}

// TestMiddlewareRejection_PoolExhausted_EnvelopeIsWireNative exercises the
// same defect through the real PoolBalanceCheck (a 402, not a 401) so the
// fix is proven against a second middleware, not just TokenAuth.
func TestMiddlewareRejection_PoolExhausted_EnvelopeIsWireNative(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-wire-pool", entity.TenantStatusEnabled)
	seedPoolR3(t, db, "t-wire-pool", 1000, 0)
	key := seedRelayUserToken(t, db, "t-wire-pool", common.UserStatusEnabled, common.TokenStatusEnabled)

	r := mountWireRejectionRouter(PoolBalanceCheck())

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("/v1/messages exhausted pool: status = %d, want 402; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf(`/v1/messages 402 body = %s, want a Claude envelope ("type":"error")`, body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("/v1/messages 402 body = %s, must not leak the OpenAI-wire error type onto the Claude wire", body)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+key)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusPaymentRequired {
		t.Fatalf("/v1/chat/completions exhausted pool: status = %d, want 402; body=%s", w2.Code, w2.Body.String())
	}
	body2 := w2.Body.String()
	if !strings.Contains(body2, `"code":"pool_exhausted"`) {
		t.Errorf(`/v1/chat/completions 402 body = %s, want "code":"pool_exhausted"`, body2)
	}
	if !strings.Contains(body2, `"tenant_id":"t-wire-pool"`) {
		t.Errorf(`/v1/chat/completions 402 body = %s, want tenant_id`, body2)
	}
}
