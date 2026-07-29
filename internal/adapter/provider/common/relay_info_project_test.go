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
