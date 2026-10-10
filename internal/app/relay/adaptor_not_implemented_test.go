package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// A converter returning (nil, nil) makes the handler marshal "null" and POST
// it upstream, billing a doomed request. Every adaptor must either build a
// real body or say ErrNotImplemented.
func TestNoAdaptorReturnsNilNilForRerankOrEmbedding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for apiType := 0; apiType < constant.APITypeDummy; apiType++ {
		a := GetAdaptor(apiType)
		if a == nil {
			continue
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		a.Init(info)

		func() {
			defer func() { _ = recover() }() // a panic is a different defect, not this sweep's
			out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
			if out == nil && err == nil {
				t.Errorf("api type %d (%T): ConvertRerankRequest returned (nil, nil)", apiType, a)
			}
		}()
		func() {
			defer func() { _ = recover() }()
			out, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
			if out == nil && err == nil {
				t.Errorf("api type %d (%T): ConvertEmbeddingRequest returned (nil, nil)", apiType, a)
			}
		}()
	}
}

func TestConvertRequestError_NotImplementedIs501SkipRetry(t *testing.T) {
	e := convertRequestError(provider.ErrNotImplemented)
	if e.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", e.StatusCode)
	}
	if e.GetErrorCode() != types.ErrorCodeAPINotImplemented {
		t.Errorf("code = %q", e.GetErrorCode())
	}
	if !types.IsSkipRetryError(e) {
		t.Error("must be skip-retry")
	}
	other := convertRequestError(errors.New("boom"))
	if other.GetErrorCode() != types.ErrorCodeConvertRequestFailed {
		t.Errorf("other code = %q", other.GetErrorCode())
	}
}
