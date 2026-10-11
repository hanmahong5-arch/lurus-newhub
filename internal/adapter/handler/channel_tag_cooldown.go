package handler

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// refreshAfterTagEnable rebuilds the channel cache after a bulk enable by tag
// and lifts the 429 cooldown of every channel the enable touched, with the same
// scope the enable itself used (root: whole tag; tenant admin: own tenant).
func refreshAfterTagEnable(c *gin.Context, tag string) {
	repo.InitChannelCache()
	var chans []*repo.Channel
	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		chans, err = repo.GetChannelsByTag(tag, false, false)
	} else {
		// GetChannelsByTagAndTenant drops the tenant filter for an empty id, which
		// would list (and un-cool) every tenant's channels under this tag.
		if c.GetString("tenant_id") == "" {
			return
		}
		chans, err = repo.GetChannelsByTagAndTenant(c.GetString("tenant_id"), tag, false)
	}
	if err != nil {
		common.SysLog("tag enable: listing channels to lift cooldown failed: " + err.Error())
		return
	}
	for _, ch := range chans {
		app.ClearChannelCooldown(ch.Id)
	}
}
