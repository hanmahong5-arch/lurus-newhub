package handler

// ============================================================================
// Business scope: small guard-rail gaps in channel.go not covered elsewhere —
// DeleteChannel's not-found branch and GetChannelKey's id-parse / not-found
// branches. GetChannelKey is behind SecureVerificationRequired in production
// routing; here we exercise the handler function directly (as the sibling
// v1 IDOR tests do) since route-level middleware is out of scope for this
// handler-level test file.
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDeleteChannel_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	c, w := handler_channel_getCtx(999999, ctx.TenantID, ctx.AdminUser.Id)
	DeleteChannel(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure deleting a non-existent channel, body=%s", w.Body.String())
	}
}

func TestGetChannelKey_GuardRails(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// non-numeric id
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	c.Set("id", ctx.AdminUser.Id)
	GetChannelKey(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for non-numeric channel id, body=%s", w.Body.String())
	}

	// channel not found
	c, w = handler_channel_getCtx(999999, ctx.TenantID, ctx.AdminUser.Id)
	GetChannelKey(c)
	body = handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for missing channel, body=%s", w.Body.String())
	}

	// success path: returns the raw key (this endpoint is intentionally
	// key-revealing, gated by SecureVerificationRequired at the route level
	// which is out of scope here).
	ch := SeedV2Channel(t, ctx, "keyreveal-chan")
	c, w = handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	GetChannelKey(c)
	body = handler_channel_mkBody(t, w)
	if body["success"] != true {
		t.Fatalf("expected success revealing key, body=%s", w.Body.String())
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok || data["key"] != ch.Key {
		t.Fatalf("expected data.key=%q, body=%s", ch.Key, w.Body.String())
	}
}
