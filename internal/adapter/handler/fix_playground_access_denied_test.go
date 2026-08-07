package handler

// fix_playground_access_denied_test.go — 回归测试：Playground 拒绝 access token
// 时返回的状态码。原实现用 types.NewError(...) 构造错误、不带任何状态码 option，
// NewError 默认回落 http.StatusInternalServerError，于是一次纯粹的权限拒绝被写成
// 500，客户端无法与真正的服务端故障区分，监控也会把它计入错误率。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestFixPlayground_AccessTokenDeniedReturns403 直接驱动 handler：上下文里带
// use_access_token=true 时必须是 4xx（403），且响应体仍带 error 结构。
func TestFixPlayground_AccessTokenDeniedReturns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/playground", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("use_access_token", true)

	Playground(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d（权限拒绝不应报成服务端错误）", w.Code, http.StatusForbidden)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("响应体缺少 error 字段: %s", w.Body.String())
	}
}
