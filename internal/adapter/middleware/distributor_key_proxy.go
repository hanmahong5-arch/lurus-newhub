package middleware

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// applyKeyProxyOverride makes the selected key's own proxy (if any) win over
// the channel-level proxy for this request: the relay reads its egress proxy
// from the ContextKeyChannelSetting value, so replacing that value is enough.
// The proxy was SSRF-checked when it was written.
func applyKeyProxyOverride(c *gin.Context, channel *repo.Channel, keyIdx int) {
	proxy := channel.KeyProxyOverride(keyIdx)
	if proxy == "" {
		return
	}
	setting, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	setting.Proxy = proxy
	common.SetContextKey(c, constant.ContextKeyChannelSetting, setting)
}
