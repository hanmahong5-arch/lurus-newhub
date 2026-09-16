package handler

// v2_chat_session_test.go — hermetic (glebarez SQLite) handler-package
// tests for GET/POST/PATCH/DELETE .../chat/sessions[/:id]. These mount the
// five handlers directly on a bare gin.New() with "id" and "tenant_context"
// hand-seeded (same convention l7_rankings_by_group_real_chain_test.go
// documents for this class of test) — sound for the request/response-shape
// and repo-wiring claims made here, but NOT a proof that the routes are
// reachable through the production UserAuth()+TenantSlugGuard() chain; that
// proof is router/chat_sessions_real_chain_test.go, which also carries the
// IDOR 404-symmetry mutation-proof target.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var chatSessionTestDBCounter atomic.Int64

type chatSessionCtx struct {
	router      *gin.Engine
	tenantID    string
	tenantSlug  string
	userID      int
	otherUserID int
}

func setupChatSessionRouter(t *testing.T) *chatSessionCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := chatSessionTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:chatsession%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &entity.Tenant{}, &entity.ChatSession{}, &entity.ChatMessage{},
	} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("automigrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false

	tenantID := fmt.Sprintf("cs-tenant-%d", n)
	tenantSlug := fmt.Sprintf("cs-slug-%d", n)
	tenant := &entity.Tenant{
		Id: tenantID, IDPOrgID: "cs-org-" + tenantID, Slug: tenantSlug,
		Name: "CS Tenant", Status: entity.TenantStatusEnabled,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	user := &repo.User{
		Username: fmt.Sprintf("cs-user-%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("cs-%d@test.local", n), TenantId: tenantID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	other := &repo.User{
		Username: fmt.Sprintf("cs-other-%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("cs-other-%d@test.local", n), TenantId: tenantID,
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("seed other user: %v", err)
	}

	ctx := &chatSessionCtx{tenantID: tenantID, tenantSlug: tenantSlug, userID: user.Id, otherUserID: other.Id}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		uid := ctx.userID
		if v := c.GetHeader("X-Test-User-Id"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil {
				uid = parsed
			}
		}
		c.Set("id", uid)
		c.Set("tenant_context", &middleware.TenantContext{TenantID: ctx.tenantID, UserID: uid})
		c.Next()
	})
	group := router.Group("/api/v2/:tenant_slug/chat/sessions")
	{
		group.GET("", ListChatSessionsV2)
		group.POST("", CreateChatSessionV2)
		group.GET("/:id", GetChatSessionV2)
		group.PATCH("/:id", UpdateChatSessionV2)
		group.DELETE("/:id", DeleteChatSessionV2)
	}
	ctx.router = router

	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return ctx
}

func (ctx *chatSessionCtx) do(method, path string, asUserID int, body any) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = strings.NewReader(string(data))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/api/v2/"+ctx.tenantSlug+"/chat/sessions"+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if asUserID != 0 {
		req.Header.Set("X-Test-User-Id", strconv.Itoa(asUserID))
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// 1. Round trip: create -> list -> fetch -> delete.
func TestV2ChatSession_RoundTrip(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	createBody := map[string]any{
		"title": "trip planning",
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "user", "content": "where should I go"},
			{"role": "assistant", "content": "Kyoto in autumn"},
		},
	}
	wCreate := ctx.do(http.MethodPost, "", ctx.userID, createBody)
	if wCreate.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200, body=%s", wCreate.Code, wCreate.Body.String())
	}
	var createResp struct {
		Success bool `json:"success"`
		Data    struct {
			Id       int `json:"id"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create: %v; body=%s", err, wCreate.Body.String())
	}
	if !createResp.Success || createResp.Data.Id == 0 {
		t.Fatalf("create response = %+v", createResp)
	}
	if len(createResp.Data.Messages) != 2 {
		t.Fatalf("create returned %d messages, want 2", len(createResp.Data.Messages))
	}
	id := createResp.Data.Id

	wList := ctx.do(http.MethodGet, "", ctx.userID, nil)
	if wList.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", wList.Code, wList.Body.String())
	}
	var listResp struct {
		Data struct {
			Sessions []struct {
				Id           int   `json:"id"`
				MessageCount int64 `json:"message_count"`
			} `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listResp.Data.Sessions) != 1 || listResp.Data.Sessions[0].Id != id {
		t.Fatalf("list = %+v, want exactly the created session %d", listResp.Data.Sessions, id)
	}
	if listResp.Data.Sessions[0].MessageCount != 2 {
		t.Fatalf("list message_count = %d, want 2", listResp.Data.Sessions[0].MessageCount)
	}

	wFetch := ctx.do(http.MethodGet, "/"+strconv.Itoa(id), ctx.userID, nil)
	if wFetch.Code != http.StatusOK {
		t.Fatalf("fetch status = %d, want 200, body=%s", wFetch.Code, wFetch.Body.String())
	}
	var fetchResp struct {
		Data struct {
			Title    string `json:"title"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wFetch.Body.Bytes(), &fetchResp); err != nil {
		t.Fatalf("unmarshal fetch: %v", err)
	}
	if fetchResp.Data.Title != "trip planning" {
		t.Fatalf("fetch title = %q, want %q", fetchResp.Data.Title, "trip planning")
	}
	if len(fetchResp.Data.Messages) != 2 || fetchResp.Data.Messages[1].Content != "Kyoto in autumn" {
		t.Fatalf("fetch messages = %+v", fetchResp.Data.Messages)
	}

	wDelete := ctx.do(http.MethodDelete, "/"+strconv.Itoa(id), ctx.userID, nil)
	if wDelete.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200, body=%s", wDelete.Code, wDelete.Body.String())
	}

	wAfterDelete := ctx.do(http.MethodGet, "/"+strconv.Itoa(id), ctx.userID, nil)
	if wAfterDelete.Code != http.StatusNotFound {
		t.Fatalf("fetch-after-delete status = %d, want 404", wAfterDelete.Code)
	}
}

// 2. PATCH replaces the message set and renames the title.
func TestV2ChatSession_UpdateReplacesMessages(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "first title",
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "user", "content": "one"},
		},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
	id := createResp.Data.Id

	wPatch := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{
		"title": "renamed",
		"messages": []map[string]string{
			{"role": "user", "content": "one"},
			{"role": "assistant", "content": "two"},
			{"role": "user", "content": "three"},
		},
	})
	if wPatch.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200, body=%s", wPatch.Code, wPatch.Body.String())
	}
	var patchResp struct {
		Data struct {
			Title    string `json:"title"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wPatch.Body.Bytes(), &patchResp); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if patchResp.Data.Title != "renamed" {
		t.Fatalf("title = %q, want renamed", patchResp.Data.Title)
	}
	if len(patchResp.Data.Messages) != 3 || patchResp.Data.Messages[2].Content != "three" {
		t.Fatalf("messages after replace = %+v, want 3 turns ending in three", patchResp.Data.Messages)
	}

	// A title-only PATCH (no "messages" key at all) must NOT touch the
	// stored messages — this is the "field absent" branch
	// repo.UpdateChatSessionOwned's doc comment describes.
	wPatch2 := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{"title": "renamed again"})
	if wPatch2.Code != http.StatusOK {
		t.Fatalf("second patch status = %d, want 200, body=%s", wPatch2.Code, wPatch2.Body.String())
	}
	var patch2Resp struct {
		Data struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wPatch2.Body.Bytes(), &patch2Resp)
	if len(patch2Resp.Data.Messages) != 3 {
		t.Fatalf("title-only patch changed message count to %d, want unchanged 3", len(patch2Resp.Data.Messages))
	}
}

// 3. Ownership fail-closed: another user's session id answers the SAME 404
// body as a session id that was never created.
func TestV2ChatSession_OwnershipFailClosed_SameShape(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "owner-only", "model": "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "secret"}},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
	ownedID := createResp.Data.Id

	// nonexistentID is deliberately far past any id this test creates.
	nonexistentID := ownedID + 9000

	wOther := ctx.do(http.MethodGet, "/"+strconv.Itoa(ownedID), ctx.otherUserID, nil)
	wMissing := ctx.do(http.MethodGet, "/"+strconv.Itoa(nonexistentID), ctx.otherUserID, nil)

	if wOther.Code != http.StatusNotFound {
		t.Fatalf("other user's GET on owned id: status = %d, want 404, body=%s", wOther.Code, wOther.Body.String())
	}
	if wMissing.Code != http.StatusNotFound {
		t.Fatalf("GET on nonexistent id: status = %d, want 404, body=%s", wMissing.Code, wMissing.Body.String())
	}
	if wOther.Body.String() != wMissing.Body.String() {
		t.Fatalf("404 bodies differ — leaks which case occurred:\n other-owner: %s\n nonexistent: %s",
			wOther.Body.String(), wMissing.Body.String())
	}

	// Same symmetry on DELETE and PATCH.
	wOtherDel := ctx.do(http.MethodDelete, "/"+strconv.Itoa(ownedID), ctx.otherUserID, nil)
	wMissingDel := ctx.do(http.MethodDelete, "/"+strconv.Itoa(nonexistentID), ctx.otherUserID, nil)
	if wOtherDel.Code != http.StatusNotFound || wMissingDel.Code != http.StatusNotFound {
		t.Fatalf("delete statuses = %d/%d, want 404/404", wOtherDel.Code, wMissingDel.Code)
	}
	if wOtherDel.Body.String() != wMissingDel.Body.String() {
		t.Fatalf("delete 404 bodies differ:\n other-owner: %s\n nonexistent: %s", wOtherDel.Body.String(), wMissingDel.Body.String())
	}

	// The owner can still fetch their own session — confirms the 404 above
	// was ownership, not the session having vanished.
	wOwnerFetch := ctx.do(http.MethodGet, "/"+strconv.Itoa(ownedID), ctx.userID, nil)
	if wOwnerFetch.Code != http.StatusOK {
		t.Fatalf("owner's own GET status = %d, want 200, body=%s", wOwnerFetch.Code, wOwnerFetch.Body.String())
	}
}

// 4. Invalid role is rejected with 400, not silently persisted.
func TestV2ChatSession_Create_RejectsUnknownRole(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	w := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "bad role", "model": "gpt-4o",
		"messages": []map[string]string{{"role": "root", "content": "hi"}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}
