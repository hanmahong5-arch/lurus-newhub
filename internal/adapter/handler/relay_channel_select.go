package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

// channelSelectionError maps a failed retry-time selection to the relay error.
// Every candidate cooling after upstream 429s is a 503 with Retry-After under
// its own code (the generic Retry-After block in Relay's deferred renderer
// emits the header from RetryAfterUnix); anything else is the old
// get_channel_failed.
func channelSelectionError(err error, modelName, group string) *types.NewAPIError {
	if errors.Is(err, app.ErrAllChannelsCooling) {
		apiErr := types.NewErrorWithStatusCode(
			fmt.Errorf("all channels for model %s in group %s are rate-limited upstream; retry in ~%ds", modelName, group, app.CoolingRetryAfterSeconds(err)),
			types.ErrorCodeAllChannelsCooling, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
		apiErr.RetryAfterUnix = time.Now().Unix() + app.CoolingRetryAfterSeconds(err)
		return apiErr
	}
	return types.NewError(fmt.Errorf("failed to select an available channel for model %s in group %s (retry): %s", modelName, group, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
}
