package router

// lutu_search_auth_test.go — regression fence for the POST /api/v2/lutu/search
// auth gap. The route proxies the paid Tavily search API; apiV2's group
// middleware is only CORS/RequestBodySizeLimit/OptionalZitaIdentity, and
// OptionalZitaIdentity never aborts an unauthenticated request, so the route
// once accepted anonymous callers who could burn the search budget. It is now
// gated by an explicit middleware.RequireOIDCToken(); an unauthenticated
// request must be rejected before reaching the handler.
//
// This test pins the ROUTE-LEVEL property (the gate is wired on the route);
// the middleware's own behaviour is covered by require_oidc_token_test.go.
// A rejection here is 503 when OIDC is disabled (the test default —
// RequireOIDCToken refuses to admit anyone if it cannot verify tokens) and
// 401 when OIDC is enabled but no/invalid bearer is presented. Both keep the
// anonymous caller off the Tavily proxy; the security property is "not 200".

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

	// No Authorization header: RequireOIDCToken fails closed before the
	// handler runs, so no Tavily call is reached.
	req := httptest.NewRequest(http.MethodPost, "/api/v2/lutu/search",
		strings.NewReader(`{"query":"anything"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unauthenticated POST /api/v2/lutu/search must be rejected (401 or, with OIDC disabled, 503), "+
			"got code=%d body=%s", w.Code, w.Body.String())
	}
	// Discriminator against a hollow pass: with OIDC disabled the gate and the
	// handler both answer 503, so status alone can't prove the gate fired.
	// PostWebSearch's disabled response carries "search_disabled"; the gate's
	// does not. Its presence means the anonymous request reached the handler —
	// i.e. the route is ungated.
	if strings.Contains(w.Body.String(), "search_disabled") {
		t.Fatalf("anonymous request reached PostWebSearch (body has search_disabled) — the Tavily proxy is unguarded: %s",
			w.Body.String())
	}
}
