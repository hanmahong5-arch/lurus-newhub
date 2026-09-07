package common

// relay_info_project_test.go — the MIDDLE hop of the cost-attribution chain
// (migration 029): gin context -> RelayInfo.
//
// RelayInfo has to carry the project for the same reason it carries
// SourceProduct: settlement (app.PostConsumeQuota -> EnrichLogParams ->
// RecordConsumeLog) runs without a gin.Context, so anything not copied here is
// simply unavailable when the log row is written.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func newRelayInfoCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestGenBaseRelayInfo_CarriesProjectId(t *testing.T) {
	c := newRelayInfoCtx()
	common.SetContextKey(c, constant.ContextKeyProjectId, 4242)

	info := genBaseRelayInfo(c, nil)

	if info.ProjectId != 4242 {
		t.Errorf("RelayInfo.ProjectId = %d, want 4242 — settlement has no gin.Context, "+
			"so a value not copied here never reaches the log row", info.ProjectId)
	}
}

func TestGenBaseRelayInfo_UnassignedProjectIsZero(t *testing.T) {
	c := newRelayInfoCtx()

	info := genBaseRelayInfo(c, nil)

	if info.ProjectId != 0 {
		t.Errorf("RelayInfo.ProjectId = %d, want 0 for a request with no project context", info.ProjectId)
	}
}

// TestGenBaseRelayInfo_CarriesSourceProduct drives the real GenRelayInfo entry
// point (not genBaseRelayInfo directly) and covers the Task and MJ formats
// explicitly — both build RelayInfo via genBaseRelayInfo(c, nil) without ever
// going through handler/relay.go's Relay(), and must carry the same
// attribution tag as the main chat path.
func TestGenBaseRelayInfo_CarriesSourceProduct(t *testing.T) {
	c := newRelayInfoCtx()
	c.Request.Header.Set(ratio_setting.SourceProductHeader, "switch")

	info, err := GenRelayInfo(c, types.RelayFormatOpenAI, nil, nil)
	if err != nil {
		t.Fatalf("GenRelayInfo: %v", err)
	}
	if info.SourceProduct != "switch" {
		t.Errorf("SourceProduct = %q, want %q for an allow-listed header", info.SourceProduct, "switch")
	}

	c2 := newRelayInfoCtx()
	c2.Request.Header.Set(ratio_setting.SourceProductHeader, "evil")
	info2, err := GenRelayInfo(c2, types.RelayFormatOpenAI, nil, nil)
	if err != nil {
		t.Fatalf("GenRelayInfo: %v", err)
	}
	if info2.SourceProduct != ratio_setting.DefaultSourceProduct {
		t.Errorf("SourceProduct = %q, want default %q for an unknown header value — "+
			"an unrecognised product must never be echoed back verbatim",
			info2.SourceProduct, ratio_setting.DefaultSourceProduct)
	}

	// The Task format (RelayTask) builds RelayInfo via genBaseRelayInfo(c, nil)
	// directly, never through Relay()'s dedicated resolution call — this is the
	// path that used to leave SourceProduct empty.
	c3 := newRelayInfoCtx()
	c3.Request.Header.Set(ratio_setting.SourceProductHeader, "lutu")
	info3, err := GenRelayInfo(c3, types.RelayFormatTask, nil, nil)
	if err != nil {
		t.Fatalf("GenRelayInfo(RelayFormatTask): %v", err)
	}
	if info3.SourceProduct != "lutu" {
		t.Errorf("Task-format SourceProduct = %q, want %q", info3.SourceProduct, "lutu")
	}

	// Same for the MJ-proxy format, which shares that branch.
	c4 := newRelayInfoCtx()
	c4.Request.Header.Set(ratio_setting.SourceProductHeader, "creator")
	info4, err := GenRelayInfo(c4, types.RelayFormatMjProxy, nil, nil)
	if err != nil {
		t.Fatalf("GenRelayInfo(RelayFormatMjProxy): %v", err)
	}
	if info4.SourceProduct != "creator" {
		t.Errorf("MJ-format SourceProduct = %q, want %q", info4.SourceProduct, "creator")
	}
}
