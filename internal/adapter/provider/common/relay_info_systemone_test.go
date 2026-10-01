package common

import (
	"testing"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestGenRelayInfoSystemOne(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/systemone")
	req := &dto.SystemOneRequest{Model: "model-a"}

	info := GenRelayInfoSystemOne(c, req)

	if info.RelayMode != relayconstant.RelayModeSystemOne {
		t.Errorf("RelayMode = %d, want RelayModeSystemOne", info.RelayMode)
	}
	if info.RelayFormat != types.RelayFormatSystemOne {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatSystemOne)
	}
	if info.Request != dto.Request(req) {
		t.Error("Request must carry the validated request through to the helper")
	}
	if info.IsStream {
		t.Error("system one never streams")
	}
}

func TestGenRelayInfo_SystemOneDispatch(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/systemone")

	info, err := GenRelayInfo(c, types.RelayFormatSystemOne, &dto.SystemOneRequest{Model: "model-a"}, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if info.RelayFormat != types.RelayFormatSystemOne {
		t.Errorf("RelayFormat = %q", info.RelayFormat)
	}

	// A mismatched request type is a wiring bug and must fail loudly rather
	// than build an info the helper cannot use.
	if _, err := GenRelayInfo(c, types.RelayFormatSystemOne, &dto.RerankRequest{}, nil); err == nil {
		t.Error("expected an error for a request that is not a SystemOneRequest")
	}
}

// The mode must come from the format, not from the URL: a request served on a
// path Path2RelayMode does not know (an alias or a channel-test call) is still
// a system one call.
func TestGenRelayInfoSystemOne_ModeDoesNotDependOnPath(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/some/alias/path")

	info := GenRelayInfoSystemOne(c, &dto.SystemOneRequest{Model: "model-a"})

	if info.RelayMode != relayconstant.RelayModeSystemOne {
		t.Errorf("RelayMode = %d, want RelayModeSystemOne regardless of path", info.RelayMode)
	}
}
