package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/capability"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// The route filter cannot import the provider adapters (repo -> provider is an
// import cycle), so internal/pkg/capability keeps a static "channel type x
// rerank" table. This test is what keeps that table honest: for every channel
// type that has an adaptor, "ConvertRerankRequest returns ErrNotImplemented"
// must be exactly "the table says rerank is unsupported". A table that says
// true for an adaptor that cannot do it would let the router pick a channel
// that fails after billing started; one that says false for a working adaptor
// would 501 a request that used to work.
//
// A panic inside a converter on an empty request means the converter reached
// real conversion code, i.e. it is implemented.
func TestCapabilityTableMatchesAdaptors_Rerank(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for channelType := 1; channelType < constant.ChannelTypeDummy; channelType++ {
		apiType, ok := common.ChannelType2APIType(channelType)
		if !ok {
			continue
		}
		a := GetAdaptor(apiType)
		if a == nil {
			continue
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		a.Init(info)

		implemented := true
		func() {
			defer func() { _ = recover() }()
			_, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
			implemented = !errors.Is(err, provider.ErrNotImplemented)
		}()
		if got := capability.SupportsRerank(channelType); got != implemented {
			t.Errorf("channel type %d (%s, %T): capability table says rerank=%v but the adaptor's ConvertRerankRequest implemented=%v",
				channelType, constant.ChannelTypeNames[channelType], a, got, implemented)
		}
	}
}
