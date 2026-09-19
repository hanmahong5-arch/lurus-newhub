package router

// r11_oauth_refresh_retired_test.go — cycle-11 L5/W: POST /api/v2/oauth/refresh
// was retired (handler.RefreshAccessToken deleted; the route registration
// removed from api-v2-router.go). Drives the real mounted v2 route table,
// not a hand-built engine.
//
// Mutation: re-adding `apiV2.POST("/oauth/refresh", handler.RefreshAccessToken)`
// to api-v2-router.go makes this test fail (404 -> 200/other), and would also
// fail to compile since the handler symbol no longer exists.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func TestOAuthRefreshRoute_Retired(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/oauth/refresh", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v2/oauth/refresh = %d, want 404 (route must no longer exist); body=%s", w.Code, w.Body.String())
	}
}
