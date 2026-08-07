package handler

// ============================================================================
// Business scope: remaining low-coverage edges in channel.go left by the
// sibling cov_handler-channel_* files — SearchChannels' status=enabled
// filter's "drop disabled" continue branch, filterChannelIdsByTenant's
// empty-input and repo-error branches, CopyChannel's invalid/not-found id
// guards, and the DB-error branches of DeleteChannelBatch / BatchSetChannelTag
// / GetTagModels / DeleteDisabledChannel (forced by closing the test's own
// isolated in-memory DB connection before the call — each test owns a
// dedicated SQLite handle from SetupV2TestRouter, so this cannot bleed into
// other tests, and V2TestContext.Cleanup's own Close() is a safe no-op on an
// already-closed handle).
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// h5a_rootCtx builds a context carrying the seeded root identity (role=root),
// required to reach the root-only branches of the batch/prune handlers below.
func h5a_rootCtx(ctx *V2TestContext, method, target string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := r2chanAdminCtx(ctx, method, target, body)
	c.Set("id", ctx.RootUser.Id)
	c.Set("role", common.RoleRootUser)
	return c, w
}

// h5a_closeDB closes the test's own isolated sqlite handle to force the next
// repo call down a real (non-mocked) DB-error branch.
func h5a_closeDB(t *testing.T, ctx *V2TestContext) {
	t.Helper()
	sqlDB, err := ctx.DB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}

// TestSearchChannels_StatusEnabledDropsDisabled proves that status=enabled
// actually excludes disabled channels from the result set (the "continue"
// branch inside the status-filter loop) rather than merely being a no-op
// filter — a regression here would leak disabled channels into consoles that
// filter by "enabled only" expecting the disabled ones scrubbed.
func TestSearchChannels_StatusEnabledDropsDisabled(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	r2chanSeedChannel(t, ctx, "s-enabled", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	r2chanSeedChannel(t, ctx, "s-disabled", constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)

	c, w := handler_channel_searchCtx(ctx, "p=1&page_size=50&status=enabled")
	SearchChannels(c)
	body := handler_channel_mkBody(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body=%s", w.Body.String())
	}
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected disabled channel dropped, got %d items body=%s", len(items), w.Body.String())
	}
	if items[0].(map[string]interface{})["name"] != "s-enabled" {
		t.Errorf("expected the surviving item to be the enabled channel, got %v", items[0])
	}
}

// TestFilterChannelIdsByTenant_EmptyAndRepoError covers the unexported
// filterChannelIdsByTenant helper's two guard branches directly: an empty
// input returns immediately (must not surface all channels), and a repo
// error (forced by a closed DB) must yield nil rather than silently widening
// scope to every id passed in.
func TestFilterChannelIdsByTenant_EmptyAndRepoError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if got := filterChannelIdsByTenant(nil, ctx.TenantID); len(got) != 0 {
		t.Fatalf("expected empty input to short-circuit to empty/nil, got %v", got)
	}

	ch := r2chanSeedChannel(t, ctx, "fbt-chan", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	h5a_closeDB(t, ctx)
	got := filterChannelIdsByTenant([]int{ch.Id}, ctx.TenantID)
	if got != nil {
		t.Fatalf("expected nil on repo error (closed DB), got %v", got)
	}
}

// TestFilterChannelIdsByTenant_KeepsOwnTenantDropsForeign is the load-bearing
// half of the helper's contract: the two guard branches above are both
// satisfied by an implementation that simply returns nil, so on their own they
// cannot tell the real filter apart from a stub. This case pins the actual
// scoping behaviour — ids belonging to the caller's tenant survive, ids
// belonging to another tenant are dropped silently (not 403'd, so a caller
// cannot probe which ids exist elsewhere).
func TestFilterChannelIdsByTenant_KeepsOwnTenantDropsForeign(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	mine := r2chanSeedChannel(t, ctx, "fbt-mine", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	mineToo := r2chanSeedChannel(t, ctx, "fbt-mine-2", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)

	foreign := &repo.Channel{
		Name:        "fbt-foreign",
		TenantId:    ctx.TenantID + "-other",
		Key:         "sk-" + common.GetRandomString(24),
		Status:      common.ChannelStatusEnabled,
		Type:        constant.ChannelTypeOpenAI,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(foreign).Error; err != nil {
		t.Fatalf("seed foreign-tenant channel: %v", err)
	}

	got := filterChannelIdsByTenant([]int{mine.Id, foreign.Id, mineToo.Id}, ctx.TenantID)

	want := map[int]bool{mine.Id: true, mineToo.Id: true}
	if len(got) != len(want) {
		t.Fatalf("filterChannelIdsByTenant returned %v, want exactly the two own-tenant ids %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("id %d leaked through the tenant filter (it belongs to %q, caller is %q)", id, foreign.TenantId, ctx.TenantID)
		}
	}
}

// TestCopyChannel_InvalidAndMissingId covers CopyChannel's non-numeric id
// parse failure and its GetChannelById not-found branch — both must fail
// cleanly with success=false rather than reaching the clone/insert path.
func TestCopyChannel_InvalidAndMissingId(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// non-numeric id param
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/copy/abc", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	CopyChannel(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for non-numeric copy id, body=%s", w.Body.String())
	}

	// well-formed but non-existent id
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/copy/999999", nil)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	CopyChannel(c)
	body = handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure copying a non-existent channel, body=%s", w.Body.String())
	}
}

// TestDeleteChannelBatch_RepoErrorPropagates forces repo.BatchDeleteChannels
// to fail (closed DB, root path skips the tenant filter so the call is
// reached directly) and proves the handler reports failure instead of
// swallowing the error and reporting success with zero effect.
func TestDeleteChannelBatch_RepoErrorPropagates(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "batch-del-err", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	h5a_closeDB(t, ctx)

	c, w := h5a_rootCtx(ctx, http.MethodPost, "/", map[string]interface{}{"ids": []int{ch.Id}})
	DeleteChannelBatch(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure when repo delete errors, body=%s", w.Body.String())
	}
}

// TestBatchSetChannelTag_RepoErrorPropagates mirrors the above for
// BatchSetChannelTag's repo.BatchSetChannelTag error branch.
func TestBatchSetChannelTag_RepoErrorPropagates(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "batch-tag-err", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	h5a_closeDB(t, ctx)

	tag := "err-tag"
	c, w := h5a_rootCtx(ctx, http.MethodPost, "/", map[string]interface{}{"ids": []int{ch.Id}, "tag": tag})
	BatchSetChannelTag(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure when repo tag-set errors, body=%s", w.Body.String())
	}
}

// TestGetTagModels_RepoErrorPropagates forces repo.GetChannelsByTag to fail
// (closed DB) and proves GetTagModels surfaces a 500 rather than silently
// returning an empty/success payload.
func TestGetTagModels_RepoErrorPropagates(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	h5a_closeDB(t, ctx)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/tag/models?tag=whatever", nil)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	GetTagModels(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on repo error, got %d body=%s", w.Code, w.Body.String())
	}
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected success=false, body=%s", w.Body.String())
	}
}

// TestDeleteDisabledChannel_RepoErrorPropagates forces the root-path
// repo.DeleteDisabledChannel() to fail (closed DB) and proves the handler
// reports failure instead of a false "success" with rows=0.
func TestDeleteDisabledChannel_RepoErrorPropagates(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	h5a_closeDB(t, ctx)

	c, w := h5a_rootCtx(ctx, http.MethodPost, "/", nil)
	DeleteDisabledChannel(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure when repo prune errors, body=%s", w.Body.String())
	}
}
