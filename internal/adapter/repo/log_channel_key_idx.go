package repo

import (
	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// NoChannelKeyIdx is what logs.channel_key_idx holds for a request that did
// not go through a multi-key channel (migration 051 default).
const NoChannelKeyIdx int64 = -1

// channelKeyIdxFromContext returns the upstream key index the relay selected
// for the current attempt. SetupContextForSelectedChannel rewrites both
// context keys on every (re)selection, and explicitly resets IsMultiKey to
// false for a single-key channel, so after a retry the value always describes
// the channel the row is being written for. Returned as a pointer: see the
// note on entity.Log.ChannelKeyIdx (GORM would swallow a bare 0).
func channelKeyIdxFromContext(c *gin.Context, channelID int) *int64 {
	idx := NoChannelKeyIdx
	// The channel guard keeps a row about channel A from carrying the key
	// index the context holds for channel B (e.g. an error row written after
	// the retry loop already moved on).
	if c != nil && common.GetContextKeyInt(c, constant.ContextKeyChannelId) == channelID &&
		common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		if v := int64(common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)); v >= 0 {
			idx = v
		}
	}
	return &idx
}

// withChannelKeyIdx stamps the key index (migration 051) onto a consume or
// error row just before it is inserted, keyed on the row's own ChannelId.
func withChannelKeyIdx(c *gin.Context, l *Log) *Log {
	l.ChannelKeyIdx = channelKeyIdxFromContext(c, l.ChannelId)
	return l
}
