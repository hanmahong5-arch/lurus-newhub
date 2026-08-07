package relay

// Business-acceptance test for WssHelper's channel-validation guard: a
// realtime (websocket) relay request whose channel is misconfigured with
// an unmapped/unknown channel type must be rejected before any attempt is
// made to dial the upstream provider or touch billing. This protects
// against a broken channel silently accepting websocket traffic that can
// never be adapted to a real upstream protocol.
//
// Self-check: stubbing WssHelper's body to `return nil` (pretend success)
// makes this test fail, because it asserts the returned error is non-nil,
// carries ErrorCodeInvalidApiType, and is marked skip-retry — none of
// which a no-op stub would produce.

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func TestWssHelper_UnknownChannelType_RejectsBeforeUpstreamDial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/v1/realtime", nil)
	// ChannelTypeAIProxyLibrary is a recognized channel type (so
	// ChannelType2APIType maps it to a real APIType instead of silently
	// falling back to OpenAI's api type), but relay.GetAdaptor has no case
	// for that api type — it's a channel the codebase accepts registering
	// but the relay layer can never actually adapt. This is the real path
	// that reaches WssHelper's "invalid api type" guard.
	c.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeAIProxyLibrary)

	info := &relaycommon.RelayInfo{}

	apiErr := WssHelper(c, info)

	if apiErr == nil {
		t.Fatal("expected WssHelper to reject a request with no resolvable channel/api type, got nil error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeInvalidApiType {
		t.Fatalf("expected ErrorCodeInvalidApiType, got %v", apiErr.GetErrorCode())
	}
	// Business rule: an invalid api type is a configuration problem, not a
	// transient upstream failure, so retry logic must not re-attempt it.
	if !types.IsSkipRetryError(apiErr) {
		t.Fatal("expected invalid-api-type error to be marked skip-retry (retrying a bad channel config can never succeed)")
	}
	// Guard against accidentally routing to a real upstream: TargetWs must
	// never be populated when the adaptor lookup fails.
	if info.TargetWs != nil {
		t.Fatal("expected no websocket connection to be established when adaptor resolution fails")
	}
}
