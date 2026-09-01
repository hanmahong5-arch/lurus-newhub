package router

// v2_user_me_handler_test.go — pins WHICH handler the production route table
// binds to GET /api/v2/:tenant_slug/user/me.
//
// This lock exists because its absence is what hid the defect. The handler
// package's test router (v2_testutil_test.go) has always registered
// `v2.GET("/user/me", GetSelfV2)`, while the production router registered
// `handler.GetSelf`. Every GetSelfV2 test was therefore green against a handler
// production never called, and the v2 dashboard shipped for months reading
// remaining_quota / token_count off a projection that has neither — telling
// metered customers they were on an unlimited plan and pinning active-keys at
// zero.
//
// Mirroring routes in a test harness is convenient and structurally blind. Any
// assertion about behaviour behind a route is only worth what this test proves:
// that the real table points at the code under test.

import (
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestSetApiV2Router_UserMeBindsGetSelfV2(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	const path = "/api/v2/:tenant_slug/user/me"
	var handlerName string
	var found bool
	for _, r := range engine.Routes() {
		if r.Method == "GET" && r.Path == path {
			handlerName = r.Handler
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GET %s is not registered at all", path)
	}

	// gin reports the fully-qualified function name of the final handler.
	if !strings.HasSuffix(handlerName, "handler.GetSelfV2") {
		t.Errorf("GET %s binds %q, want …handler.GetSelfV2.\n"+
			"handler.GetSelf (the v1 projection) has neither remaining_quota nor token_count, which the v2 "+
			"dashboard reads directly (web/src/pages/v2/Dashboard/index.jsx:200,219); binding it here makes "+
			"the balance card claim an unlimited plan and the active-keys card read 0 while the keys page "+
			"lists keys.", path, handlerName)
	}
}
