package handler

// fix_health_redis_label_test.go — 回归测试：/api/health 的 redis 标签。
// 原实现把「Redis 被显式关闭」和「Redis 打开但客户端为 nil（接线坏了）」
// 折叠成同一个 "disabled"，运维读健康检查无法分辨真实故障。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// fixHealthRedisState 设定 Redis 全局态与空的 repo.DB（DB 分支不是本用例关注点），
// 结束时恢复。
func fixHealthRedisState(t *testing.T, enabled bool) {
	t.Helper()
	prevRDB, prevEnabled, prevDB := common.RDB, common.RedisEnabled, repo.DB
	common.RDB = nil
	common.RedisEnabled = enabled
	repo.DB = nil
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, repo.DB = prevRDB, prevEnabled, prevDB
	})
}

// fixHealthChecks 调 GetHealthDetailed 并返回 checks 映射。
func fixHealthChecks(t *testing.T) map[string]string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	GetHealthDetailed(c)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return body.Checks
}

// TestFixHealth_RedisEnabledButNilClientIsNotConfigured：打开但没客户端 =>
// 独立标签 not_configured，而不是与「有意关闭」同名。
func TestFixHealth_RedisEnabledButNilClientIsNotConfigured(t *testing.T) {
	fixHealthRedisState(t, true)
	checks := fixHealthChecks(t)
	if got := checks["redis"]; got != "not_configured" {
		t.Fatalf("checks.redis = %q, want \"not_configured\"（坏接线被伪装成有意关闭）", got)
	}
}

// TestFixHealth_RedisDisabledKeepsDisabledLabel：有意关闭时标签不变，
// 避免修复反过来把正常的「不用 Redis」部署报成故障。
func TestFixHealth_RedisDisabledKeepsDisabledLabel(t *testing.T) {
	fixHealthRedisState(t, false)
	checks := fixHealthChecks(t)
	if got := checks["redis"]; got != "disabled" {
		t.Fatalf("checks.redis = %q, want \"disabled\"", got)
	}
}
