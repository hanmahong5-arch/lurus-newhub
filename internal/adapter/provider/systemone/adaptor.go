package systemone

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// Adaptor serves ChannelTypeTypeSafe and ChannelTypeSystemOneCompatible. Only
// POST /v1/systemone is supported, so every other Convert* stays the
// BaseAdaptor's not-implemented default.
type Adaptor struct {
	provider.BaseAdaptor
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil {
		return "", errors.New("systemone adaptor: relay info is nil")
	}
	base := info.ChannelBaseUrl
	if base == "" && info.ChannelType == constant.ChannelTypeTypeSafe {
		base = constant.ChannelBaseURLs[constant.ChannelTypeTypeSafe]
	}
	if base == "" {
		return "", errors.New("systemone adaptor: base URL is required for self-hosted channels")
	}
	return strings.TrimRight(base, "/") + requestPath, nil
}

// SetupRequestHeader sends only what the upstream needs: the caller's own
// headers (hub key, SDK identification) are never forwarded. A self-hosted
// server may run without auth, so the Bearer is omitted when there is no key.
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	if info == nil {
		return errors.New("systemone adaptor: relay info is nil")
	}
	req.Set("Content-Type", "application/json")
	req.Set("Accept", "application/json")
	if info.ApiKey != "" {
		req.Set("Authorization", "Bearer "+info.ApiKey)
	}
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return provider.DoApiRequest(a, c, info, requestBody) //nolint:bodyclose // resp is handed to the relay layer, which owns closing it.
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
