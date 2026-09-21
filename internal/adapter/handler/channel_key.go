package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// channel_key.go — GetChannelKey, split out of channel.go by the cycle-13
// wiring pass. Pure move: the function is byte-identical to the one that
// stood in channel.go, and nothing else changed. The split exists because
// internal/pkg/gates' source-size ratchet holds channel.go at its measured
// line count, and this cycle's additions to the file have to be paid for by
// a move rather than by raising the ceiling. Reveal is its own concern
// anyway: it is the only channel handler that returns a provider secret, it
// is root-gated and audited separately from the CRUD handlers around it.

// GetChannelKey 返回渠道的上游密钥。路由 POST /api/channel/:id/key 受
// RootAuth(admin-only) + CriticalRateLimit + SecureVerificationRequired 保护:
// 调用方须先 POST /api/verify 通过会话级二次验证(5 分钟有效),否则中间件返回
// 403 VERIFICATION_REQUIRED。强认证因子(MFA)在 OIDC IdP 登录完成,本地二次验证
// 为敏感操作的会话级再确认。
func GetChannelKey(c *gin.Context) {
	userId := c.GetInt("id")
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("渠道ID格式错误: %w", err))
		return
	}

	// 获取渠道信息（包含密钥）
	channel, err := repo.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, fmt.Errorf("获取渠道信息失败: %w", err))
		return
	}

	if channel == nil {
		common.ApiError(c, fmt.Errorf("渠道不存在"))
		return
	}

	// 记录操作日志
	repo.RecordLog(userId, repo.LogTypeSystem, fmt.Sprintf("查看渠道密钥信息 (渠道ID: %d)", channelId))

	// Security audit trail (cycle 13 L4, V1DOORS/SECURITY-14): the RecordLog
	// line above is a logs row, and DELETE /api/log/ can remove logs rows —
	// this call goes to the separate audit_events table, which DELETE
	// /api/log/ cannot touch (its own retention sweep,
	// lifecycle/audit_cleanup.go, is the path that expires these rows).
	// Details carry the channel id and its tenant only; channel.Key never
	// enters Details — that field only appears in the JSON response below,
	// which the caller already passed SecureVerificationRequired to see.
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, userId,
		governance.ActionChannelKeyAccessed, governance.ResourceChannel, channelId,
		fmt.Sprintf(`{"channel_id":%d,"tenant_id":%q}`, channelId, channel.TenantId)))

	// 返回渠道密钥
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取成功",
		"data": map[string]interface{}{
			"key": channel.Key,
		},
	})
}
