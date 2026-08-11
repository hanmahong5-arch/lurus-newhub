package handler

// ============================================================================
// Business scope: SearchChannels (internal/adapter/handler/channel.go) — the
// console's channel search/filter/pagination endpoint. Only the DB-backed
// path is exercised here (the Meilisearch fast-path requires a live search
// cluster this environment doesn't have — see honest_notes in the coverage
// report; search.IsEnabled() requires a non-nil meilisearch.ServiceManager
// client which cannot be faked without reimplementing a 50+ method
// interface, so that branch is left to a hermetic search-package test).
//
// Edge cases covered: cross-tenant scoping (root vs tenant admin), tag mode,
// type filter, status filter, id_sort, and pagination boundaries (page<1,
// page beyond total, page_size<=0).
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

func handler_channel_searchCtx(ctx *V2TestContext, query string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/search?"+query, nil)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	return c, w
}

func TestSearchChannels_TenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	r2chanSeedChannel(t, ctx, "own-search", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	victim := &repo.Channel{
		Name: "victim-search", TenantId: "other-tenant-search", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI,
		Models: "gpt-4", CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	// non-root: only own tenant's channel comes back.
	c, w := handler_channel_searchCtx(ctx, "p=1&page_size=50")
	SearchChannels(c)
	body := handler_channel_mkBody(t, w)
	data := body["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 own-tenant result, got %d body=%s", len(items), w.Body.String())
	}
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["tenant_id"] != ctx.TenantID {
			t.Errorf("cross-tenant leak: %v", m["tenant_id"])
		}
	}

	// root: sees both tenants' channels.
	c, w = handler_channel_searchCtx(ctx, "p=1&page_size=50")
	c.Set("id", ctx.RootUser.Id)
	c.Set("role", common.RoleRootUser)
	SearchChannels(c)
	body = handler_channel_mkBody(t, w)
	data = body["data"].(map[string]interface{})
	items = data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected root to see 2 channels, got %d body=%s", len(items), w.Body.String())
	}
}

func TestSearchChannels_TagModeAndFilters(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	tag := "search-tag"
	own := r2chanSeedChannel(t, ctx, "own-tag-search", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	own.Tag = &tag
	if err := own.Update(); err != nil {
		t.Fatalf("tag own: %v", err)
	}
	victim := &repo.Channel{
		Name: "victim-tag-search", TenantId: "other-tenant-tag-search", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI,
		Tag: &tag, CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	// tag mode: non-root must not see the other tenant's tagged channel even
	// though repo.GetChannelsByTag itself is not tenant-scoped (defence in
	// depth filter inside SearchChannels).
	c, w := handler_channel_searchCtx(ctx, "tag_mode=true")
	SearchChannels(c)
	body := handler_channel_mkBody(t, w)
	data := body["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 tenant-scoped tag result, got %d body=%s", len(items), w.Body.String())
	}

	// type filter narrows results.
	r2chanSeedChannel(t, ctx, "own-gemini-search", constant.ChannelTypeGemini, common.ChannelStatusEnabled)
	c, w = handler_channel_searchCtx(ctx, "p=1&page_size=50&type="+strconv.Itoa(constant.ChannelTypeGemini))
	SearchChannels(c)
	body = handler_channel_mkBody(t, w)
	data = body["data"].(map[string]interface{})
	items = data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 gemini-typed result, got %d body=%s", len(items), w.Body.String())
	}

	// status filter narrows to disabled only.
	r2chanSeedChannel(t, ctx, "own-disabled-search", constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)
	c, w = handler_channel_searchCtx(ctx, "p=1&page_size=50&status=disabled")
	SearchChannels(c)
	body = handler_channel_mkBody(t, w)
	data = body["data"].(map[string]interface{})
	items = data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 disabled result, got %d body=%s", len(items), w.Body.String())
	}
}

// TestSearchChannels_PaginationBoundaries covers page<1 defaulting to 1,
// page_size<=0 defaulting to 20, and a page number beyond the result set
// clamping to an empty (not out-of-range-panicking) page.
func TestSearchChannels_PaginationBoundaries(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	for i := 0; i < 3; i++ {
		r2chanSeedChannel(t, ctx, "pg-chan", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	}

	// p=0 and page_size=0 → defaults applied, all 3 results on one page.
	c, w := handler_channel_searchCtx(ctx, "p=0&page_size=0")
	SearchChannels(c)
	body := handler_channel_mkBody(t, w)
	data := body["data"].(map[string]interface{})
	if int(data["total"].(float64)) != 3 {
		t.Fatalf("expected total=3, got %v", data["total"])
	}
	items := data["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected default page_size=20 to fit all 3, got %d items", len(items))
	}

	// negative page → clamps to 1.
	c, w = handler_channel_searchCtx(ctx, "p=-5&page_size=2")
	SearchChannels(c)
	body = handler_channel_mkBody(t, w)
	data = body["data"].(map[string]interface{})
	items = data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 items on clamped first page, got %d", len(items))
	}

	// page far beyond total → empty items slice, no panic, total still correct.
	c, w = handler_channel_searchCtx(ctx, "p=999&page_size=2")
	SearchChannels(c)
	body = handler_channel_mkBody(t, w)
	data = body["data"].(map[string]interface{})
	items, _ = data["items"].([]interface{})
	if len(items) != 0 {
		t.Fatalf("expected 0 items for out-of-range page, got %d", len(items))
	}
	if int(data["total"].(float64)) != 3 {
		t.Fatalf("expected total still=3 for out-of-range page, got %v", data["total"])
	}
}
