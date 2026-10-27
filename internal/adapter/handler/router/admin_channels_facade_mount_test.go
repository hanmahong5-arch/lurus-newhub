package router

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestSetApiV2Router_AdminChannelsFacadeMounted locks the thin
// /api/v2/admin/channels (+ groups / prefill-groups) facade that lets the
// ops-channels console leave deprecated /api/channel.
func TestSetApiV2Router_AdminChannelsFacadeMounted(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	has := func(method, path string) bool {
		for _, rt := range engine.Routes() {
			if rt.Method == method && rt.Path == path {
				return true
			}
		}
		return false
	}

	required := []struct{ method, path string }{
		{"GET", "/api/v2/admin/groups"},
		{"GET", "/api/v2/admin/prefill-groups"},
		{"GET", "/api/v2/admin/channels"},
		{"GET", "/api/v2/admin/channels/search"},
		{"GET", "/api/v2/admin/channels/models"},
		{"GET", "/api/v2/admin/channels/models_enabled"},
		{"GET", "/api/v2/admin/channels/test"},
		{"GET", "/api/v2/admin/channels/test/:id"},
		{"GET", "/api/v2/admin/channels/update_balance"},
		{"GET", "/api/v2/admin/channels/update_balance/:id"},
		{"POST", "/api/v2/admin/channels"},
		{"PUT", "/api/v2/admin/channels"},
		{"DELETE", "/api/v2/admin/channels/disabled"},
		{"POST", "/api/v2/admin/channels/batch"},
		{"POST", "/api/v2/admin/channels/batch/tag"},
		{"POST", "/api/v2/admin/channels/fix"},
		{"GET", "/api/v2/admin/channels/fetch_models/:id"},
		{"POST", "/api/v2/admin/channels/fetch_models"},
		{"POST", "/api/v2/admin/channels/ollama/pull"},
		{"POST", "/api/v2/admin/channels/ollama/pull/stream"},
		{"DELETE", "/api/v2/admin/channels/ollama/delete"},
		{"GET", "/api/v2/admin/channels/ollama/version/:id"},
		{"POST", "/api/v2/admin/channels/tag/enabled"},
		{"POST", "/api/v2/admin/channels/tag/disabled"},
		{"PUT", "/api/v2/admin/channels/tag"},
		{"GET", "/api/v2/admin/channels/tag/models"},
		{"POST", "/api/v2/admin/channels/copy/:id"},
		{"POST", "/api/v2/admin/channels/multi_key/manage"},
		{"GET", "/api/v2/admin/channels/:id"},
		{"DELETE", "/api/v2/admin/channels/:id"},
		{"POST", "/api/v2/admin/channels/:id/key"},
	}
	for _, c := range required {
		if !has(c.method, c.path) {
			t.Errorf("missing facade route %s %s — ops-channels would 404", c.method, c.path)
		}
	}
}
