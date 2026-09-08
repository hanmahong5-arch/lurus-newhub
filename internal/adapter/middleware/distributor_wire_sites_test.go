package middleware

// distributor_wire_sites_test.go — L3-CONTRACT-TAXONOMY item 1: per-site
// wire assertions through the real Distribute middleware (mounted the same
// way mountDistributeModelLimit does — StampRelayFormat ahead of Distribute
// so renderRejection picks the caller's own envelope) for the four abort
// sites distributor.go :77, :85, :114, :129, pinning the corrected code each
// one now carries instead of the historical empty code string.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// mountDistributeWire is mountDistribute's sibling with StampRelayFormat
// mounted first, so a wire-native (Claude/OpenAI) envelope renders instead
// of the format-less default — mountDistribute alone never stamps a format,
// so a test using it can only observe the OpenAI-default envelope shape.
func mountDistributeWire(ctxSetup func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if ctxSetup != nil {
			ctxSetup(c)
		}
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat())
	v1.POST("/chat/completions", Distribute(), pass)
	pg := r.Group("/pg")
	pg.Use(StampRelayFormat())
	pg.POST("/chat/completions", Distribute(), pass)
	return r
}

func postJSON(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// distributor.go :77 — a pinned channel with no resolvable caller tenant
// context must 403 channel_specify_forbidden, not the old empty code.
func TestDistribute_PinnedChannel_NoTenantContext_ChannelSpecifyForbidden(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{
		Id: 41, Name: "pin-no-tenant-ctx", TenantId: "default", Key: "k",
		Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default",
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	r := mountDistributeWire(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "41")
		// Deliberately NOT setting tenant_context — GetTenantContext must
		// error, which the fail-CLOSED branch turns into 403.
	})
	w := postJSON(r, "/v1/chat/completions", `{"model":"gpt-4o"}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":"channel_specify_forbidden"`) {
		t.Errorf("body = %s, want error.code channel_specify_forbidden", body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("body = %s, must not leak the retired new_api_error literal", body)
	}
}

// distributor.go :85 — a pinned channel owned by a DIFFERENT tenant than
// the caller must also 403 channel_specify_forbidden (the cross-tenant
// exfiltration guard, distinct code path from :77 above — both share the
// same code, so the message substring below is what actually pins this
// branch instead of the :77 one).
func TestDistribute_PinnedChannel_CrossTenant_ChannelSpecifyForbidden(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{
		Id: 42, Name: "pin-cross-tenant", TenantId: "other-tenant", Key: "k",
		Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default",
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	r := mountDistributeWire(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "42")
		c.Set("tenant_context", &TenantContext{TenantID: "caller-tenant"})
	})
	w := postJSON(r, "/v1/chat/completions", `{"model":"gpt-4o"}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":"channel_specify_forbidden"`) {
		t.Errorf("body = %s, want error.code channel_specify_forbidden", body)
	}
	if !strings.Contains(body, "another tenant's channel") {
		t.Errorf("body = %s, want the cross-tenant message (distinguishes :85 from :77)", body)
	}
}

// distributor.go :114 — a select-channel request with no model name at all
// must 400 invalid_request.
func TestDistribute_ModelNameEmpty_InvalidRequest(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistributeWire(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := postJSON(r, "/v1/chat/completions", `{}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":"invalid_request"`) {
		t.Errorf("body = %s, want error.code invalid_request", body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("body = %s, must not leak the retired new_api_error literal", body)
	}
}

// distributor.go :129 — a playground request naming a group the caller's
// group cannot use must 403 group_not_allowed.
func TestDistribute_Playground_GroupNotAllowed(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistributeWire(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "")
	})
	w := postJSON(r, "/pg/chat/completions", `{"model":"gpt-4o","group":"definitely-not-a-real-group-xyz"}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":"group_not_allowed"`) {
		t.Errorf("body = %s, want error.code group_not_allowed", body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("body = %s, must not leak the retired new_api_error literal", body)
	}
}
