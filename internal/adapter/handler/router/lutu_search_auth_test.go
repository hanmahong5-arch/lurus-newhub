package router

// lutu_search_auth_test.go — regression fence for the POST /api/v2/lutu/search
// auth gap. The route proxies the paid Tavily search API; apiV2's group
// middleware is only CORS/RequestBodySizeLimit/OptionalZitaIdentity, and
// OptionalZitaIdentity never aborts an unauthenticated request, so the route
// once accepted anonymous callers who could burn the search budget. It is now
// gated by an explicit middleware.TokenAuth(); an unauthenticated request must
// be rejected before reaching the handler.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestLutuSearch_RequiresAuth(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	// No Authorization header: TokenAuth resolves an empty key and fails
	// closed before the handler runs, so no DB or Tavily call is reached.
	req := httptest.NewRequest(http.MethodPost, "/api/v2/lutu/search",
		strings.NewReader(`{"query":"anything"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /api/v2/lutu/search must be rejected with 401, got code=%d body=%s",
			w.Code, w.Body.String())
	}
}
