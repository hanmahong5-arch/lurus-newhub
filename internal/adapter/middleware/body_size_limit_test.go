package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// mountBodySizeLimitEcho builds a minimal router carrying only
// RequestBodySizeLimit() + a bare ShouldBindJSON handler, per the task's
// instruction to avoid depending on real business handlers. This isolates the
// assertion to the middleware's own read-limit behavior.
func mountBodySizeLimitEcho() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestBodySizeLimit())
	r.POST("/echo", func(c *gin.Context) {
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// TestRequestBodySizeLimit_SmallBody_Allowed proves the fix does not break
// normal-sized requests (defect #8's red line: small POST/PUT must keep
// working unchanged).
func TestRequestBodySizeLimit_SmallBody_Allowed(t *testing.T) {
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1 // 1MB cap
	defer func() { constant.MaxRequestBodyMB = prev }()

	r := mountBodySizeLimitEcho()
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader([]byte(`{"hello":"world"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a small body under the cap; body=%s", w.Code, w.Body.String())
	}
}

// TestRequestBodySizeLimit_OversizedBody_Rejected proves an over-cap body is
// rejected at read time (ShouldBindJSON errors) instead of being fully
// buffered/parsed — the DoS this middleware closes off for /api and /api/v2.
func TestRequestBodySizeLimit_OversizedBody_Rejected(t *testing.T) {
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1 // 1MB cap
	defer func() { constant.MaxRequestBodyMB = prev }()

	r := mountBodySizeLimitEcho()
	// (1<<20) = 1MB cap; pad well past it.
	oversizedValue := bytes.Repeat([]byte("a"), (1<<20)+1024)
	body := append([]byte(`{"pad":"`), oversizedValue...)
	body = append(body, []byte(`"}`)...)

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (oversized body must be rejected, not fully read); body=%s", w.Code, w.Body.String())
	}
}
