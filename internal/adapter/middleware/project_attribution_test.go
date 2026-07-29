package middleware

// project_attribution_test.go — the FIRST hop of the cost-attribution chain
// (migration 029): tokens.project_id -> gin context.
//
// SetupContextForToken is the only place a token's project enters a request.
// Everything downstream (RelayInfo.ProjectId, governance.EnrichLogParams, the
// logs.project_id column) reads from what is set here, and attribution can
// never be backfilled — so a token whose project fails to land in the context
// produces permanently unattributable spend.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func newProjectAttributionCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestSetupContextForToken_CarriesProjectId(t *testing.T) {
	c := newProjectAttributionCtx()
	tok := &repo.Token{Id: 11, UserId: 5, TenantId: "tenant-a", Name: "tagged", ProjectId: 4242}

	if err := SetupContextForToken(c, tok); err != nil {
		t.Fatalf("SetupContextForToken: %v", err)
	}
	if got := common.GetContextKeyInt(c, constant.ContextKeyProjectId); got != 4242 {
		t.Errorf("context project id = %d, want 4242", got)
	}
}

// An unassigned token must still SET the key — to 0. Leaving it absent and
// relying on GetContextKeyInt's zero value would be the same observable
// result today, but it makes the "is this request attributed?" question
// unanswerable from the context alone.
func TestSetupContextForToken_UnassignedTokenSetsZero(t *testing.T) {
	c := newProjectAttributionCtx()
	tok := &repo.Token{Id: 12, UserId: 5, TenantId: "tenant-a", Name: "untagged"}

	if err := SetupContextForToken(c, tok); err != nil {
		t.Fatalf("SetupContextForToken: %v", err)
	}
	raw, exists := c.Get(string(constant.ContextKeyProjectId))
	if !exists {
		t.Fatal("project id key absent from context; it must always be set, even when unassigned")
	}
	if v, ok := raw.(int); !ok || v != 0 {
		t.Errorf("context project id = %v (%T), want int 0", raw, raw)
	}
}
