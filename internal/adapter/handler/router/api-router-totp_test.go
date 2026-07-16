package router

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestSetApiRouter_TotpWiring asserts the TOTP step-up factor endpoints are
// mounted to match the frontend contract (web/src/services/secureVerification.js
// TotpService): status/enroll/confirm/disable under /api/user/totp/*.
func TestSetApiRouter_TotpWiring(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	SetApiRouter(engine)

	has := func(method, path string) bool {
		for _, rt := range engine.Routes() {
			if rt.Method == method && rt.Path == path {
				return true
			}
		}
		return false
	}

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/user/totp/status"},
		{"POST", "/api/user/totp/enroll"},
		{"POST", "/api/user/totp/confirm"},
		{"POST", "/api/user/totp/disable"},
	} {
		if !has(c.method, c.path) {
			t.Errorf("route %s %s not registered", c.method, c.path)
		}
	}
}
