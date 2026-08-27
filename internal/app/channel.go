package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := repo.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
		NotifyRootUser(context.TODO(), formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
		governance.RecordAuditEvent(&entity.AuditEvent{
			TenantID:   "default",
			Timestamp:  common.GetTimestamp(),
			ActorType:  governance.ActorSystem,
			Action:     governance.ActionChannelDisabled,
			Resource:   governance.ResourceChannel,
			ResourceID: channelError.ChannelId,
			Details: func() string {
				b, _ := json.Marshal(map[string]string{"name": channelError.ChannelName, "reason": reason})
				return string(b)
			}(),
		})
	}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := repo.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		NotifyRootUser(context.TODO(), formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
}

func ShouldDisableChannel(channelType int, err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if err.StatusCode == http.StatusUnauthorized {
		return true
	}
	if err.StatusCode == http.StatusForbidden {
		switch channelType {
		case constant.ChannelTypeGemini:
			return true
		}
	}
	oaiErr := err.ToOpenAIError()
	switch oaiErr.Code {
	case "invalid_api_key":
		return true
	case "account_deactivated":
		return true
	case "billing_not_active":
		return true
	case "pre_consume_token_quota_failed":
		return true
	case "Arrearage":
		return true
	// token_quota_exhausted (R2/B2): auth.go and PreConsumeQuota both emit
	// this error with type="new_api_error"/code="token_quota_exhausted" (see
	// NewErrorWithStatusCode + ToOpenAIError's default branch, which always
	// sets Type to the ErrorType constant, never the ErrorCode). A downstream
	// newhub instance relaying through this one as an upstream channel
	// therefore observes it here, not in the oaiErr.Type switch below — it
	// belongs in this Code switch, not the Type one (verified by round-trip:
	// marshal the real 402 body, re-parse via RelayErrorHandler, see
	// r2_channel_token_quota_test.go).
	case "token_quota_exhausted":
		return true
	}
	switch oaiErr.Type {
	case "insufficient_quota":
		return true
	case "insufficient_user_quota":
		return true
	// https://docs.anthropic.com/claude/reference/errors
	case "authentication_error":
		return true
	case "permission_error":
		return true
	case "forbidden":
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
