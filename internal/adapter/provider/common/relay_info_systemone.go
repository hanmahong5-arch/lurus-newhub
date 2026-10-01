package common

import (
	"errors"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// GenRelayInfoSystemOne builds the relay info for POST /v1/systemone. The
// request never streams, so IsStream keeps its zero value.
func GenRelayInfoSystemOne(c *gin.Context, request *dto.SystemOneRequest) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayMode = relayconstant.RelayModeSystemOne
	info.RelayFormat = types.RelayFormatSystemOne
	return info
}

// systemOneRelayInfo is the GenRelayInfo dispatch target: it rejects a request
// of the wrong type instead of building an info the helper cannot use.
func systemOneRelayInfo(c *gin.Context, request dto.Request) (*RelayInfo, error) {
	req, ok := request.(*dto.SystemOneRequest)
	if !ok {
		return nil, errors.New("request is not a SystemOneRequest")
	}
	return GenRelayInfoSystemOne(c, req), nil
}
