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
	"time"

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
	db          *gorm.DB
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

	ctx := &chatSessionCtx{db: db, tenantID: tenantID, tenantSlug: tenantSlug, userID: user.Id, otherUserID: other.Id}

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

// 5. An oversized title is rejected with 400, not silently truncated — pins
// maxChatSessionTitleRunes's doc comment as the actual behaviour, not just
// its claim. A title exactly at the 255-rune limit must still succeed
// (boundary is >, not >=).
func TestV2ChatSession_Create_RejectsOversizedTitle(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	oversized := strings.Repeat("x", maxChatSessionTitleRunes+1)
	w := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": oversized, "model": "gpt-4o", "messages": []map[string]string{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized title status = %d, want 400, body=%s", w.Code, w.Body.String())
	}

	atLimit := strings.Repeat("y", maxChatSessionTitleRunes)
	wOK := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": atLimit, "model": "gpt-4o", "messages": []map[string]string{},
	})
	if wOK.Code != http.StatusOK {
		t.Fatalf("at-limit title status = %d, want 200, body=%s", wOK.Code, wOK.Body.String())
	}
}

// 6. PATCH applies the same oversized-title rejection as create, and a
// rejected PATCH must not have applied.
func TestV2ChatSession_Update_RejectsOversizedTitle(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "short", "model": "gpt-4o", "messages": []map[string]string{},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)

	oversized := strings.Repeat("z", maxChatSessionTitleRunes+1)
	wPatch := ctx.do(http.MethodPatch, "/"+strconv.Itoa(createResp.Data.Id), ctx.userID, map[string]any{
		"title": oversized,
	})
	if wPatch.Code != http.StatusBadRequest {
		t.Fatalf("oversized patch title status = %d, want 400, body=%s", wPatch.Code, wPatch.Body.String())
	}

	wFetch := ctx.do(http.MethodGet, "/"+strconv.Itoa(createResp.Data.Id), ctx.userID, nil)
	var fetchResp struct {
		Data struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wFetch.Body.Bytes(), &fetchResp)
	if fetchResp.Data.Title != "short" {
		t.Fatalf("title after rejected patch = %q, want unchanged %q", fetchResp.Data.Title, "short")
	}
}

// 7. PATCH's response must reflect the FRESH row the write just performed,
// not the pre-update struct UpdateChatSessionOwned read at the start of its
// own transaction (repo.UpdateChatSessionOwned sets `session.Title` in
// memory but never `session.UpdatedAt` — only the DB row gets a fresh
// updated_at, via a raw map-based Updates call). Two PATCHes in a row expose
// the regression: without the re-read fix, the SECOND PATCH's response would
// echo the FIRST PATCH's updated_at instead of its own.
func TestV2ChatSession_Update_ResponseReflectsFreshUpdatedAt(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "v1", "model": "gpt-4o", "messages": []map[string]string{},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
	id := createResp.Data.Id

	type patchDetail struct {
		Data struct {
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"data"`
	}

	time.Sleep(2 * time.Millisecond)
	wPatch1 := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{"title": "v2"})
	var patch1Resp patchDetail
	if err := json.Unmarshal(wPatch1.Body.Bytes(), &patch1Resp); err != nil {
		t.Fatalf("unmarshal patch1: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	wPatch2 := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{"title": "v3"})
	var patch2Resp patchDetail
	if err := json.Unmarshal(wPatch2.Body.Bytes(), &patch2Resp); err != nil {
		t.Fatalf("unmarshal patch2: %v", err)
	}

	wGet := ctx.do(http.MethodGet, "/"+strconv.Itoa(id), ctx.userID, nil)
	var getResp patchDetail
	if err := json.Unmarshal(wGet.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}

	if !patch2Resp.Data.UpdatedAt.Equal(getResp.Data.UpdatedAt) {
		t.Fatalf("second PATCH response updated_at = %v, want it to match a fresh GET's %v",
			patch2Resp.Data.UpdatedAt, getResp.Data.UpdatedAt)
	}
	if patch2Resp.Data.UpdatedAt.Equal(patch1Resp.Data.UpdatedAt) {
		t.Fatalf("second PATCH response updated_at (%v) equals the FIRST patch's (%v) — stale pre-update struct was echoed back",
			patch2Resp.Data.UpdatedAt, patch1Resp.Data.UpdatedAt)
	}
}

func chatSessionErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v; body=%s", err, w.Body.String())
	}
	return resp.ErrorCode
}

func chatSessionMessagesOfLen(n int) []map[string]string {
	out := make([]map[string]string, n)
	for i := range out {
		out[i] = map[string]string{"role": "user", "content": "turn"}
	}
	return out
}

// 8. maxChatSessionsPerUser is enforced on create: the 201st session for one
// user 400s with CHAT_SESSION_LIMIT_REACHED, while a DIFFERENT user in the
// same tenant is unaffected (the cap is per-user, not per-tenant).
func TestV2ChatSession_Create_RejectsWhenSessionCapReached(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	rows := make([]entity.ChatSession, repo.MaxChatSessionsPerUser)
	for i := range rows {
		rows[i] = entity.ChatSession{TenantId: ctx.tenantID, UserId: ctx.userID, Title: fmt.Sprintf("seed-%d", i), Model: "rt-alpha"}
	}
	if err := ctx.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed %d sessions: %v", repo.MaxChatSessionsPerUser, err)
	}

	w := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "one too many", "model": "rt-alpha", "messages": []map[string]string{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("201st create for the capped user: status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
	if code := chatSessionErrorCode(t, w); code != "CHAT_SESSION_LIMIT_REACHED" {
		t.Fatalf("201st create error_code = %q, want CHAT_SESSION_LIMIT_REACHED", code)
	}

	wOther := ctx.do(http.MethodPost, "", ctx.otherUserID, map[string]any{
		"title": "a different user's first session", "model": "rt-alpha", "messages": []map[string]string{},
	})
	if wOther.Code != http.StatusOK {
		t.Fatalf("create for a different user in the same tenant: status = %d, want 200, body=%s", wOther.Code, wOther.Body.String())
	}
}

// 9. maxChatSessionMessages is enforced on create: 501 turns 400s, exactly
// 500 succeeds (boundary is >, not >=).
func TestV2ChatSession_Create_RejectsOversizedMessageList(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wOver := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "too many turns", "model": "rt-alpha", "messages": chatSessionMessagesOfLen(501),
	})
	if wOver.Code != http.StatusBadRequest {
		t.Fatalf("501-message create: status = %d, want 400, body=%s", wOver.Code, wOver.Body.String())
	}
	if code := chatSessionErrorCode(t, wOver); code != "INVALID_REQUEST" {
		t.Fatalf("501-message create error_code = %q, want INVALID_REQUEST", code)
	}

	wAtLimit := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "at the limit", "model": "rt-alpha", "messages": chatSessionMessagesOfLen(500),
	})
	if wAtLimit.Code != http.StatusOK {
		t.Fatalf("500-message create: status = %d, want 200, body=%s", wAtLimit.Code, wAtLimit.Body.String())
	}
}

// 10. The same maxChatSessionMessages ceiling applies to PATCH (both routes
// share parseChatSessionMessages), and a REJECTED patch must leave the
// stored session's messages untouched.
func TestV2ChatSession_Update_RejectsOversizedMessageList(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "starter", "model": "rt-alpha",
		"messages": []map[string]string{{"role": "user", "content": "one"}},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
	id := createResp.Data.Id

	wOver := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{
		"messages": chatSessionMessagesOfLen(501),
	})
	if wOver.Code != http.StatusBadRequest {
		t.Fatalf("501-message patch: status = %d, want 400, body=%s", wOver.Code, wOver.Body.String())
	}

	wFetch := ctx.do(http.MethodGet, "/"+strconv.Itoa(id), ctx.userID, nil)
	var fetchResp struct {
		Data struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wFetch.Body.Bytes(), &fetchResp)
	if len(fetchResp.Data.Messages) != 1 || fetchResp.Data.Messages[0].Content != "one" {
		t.Fatalf("messages after rejected patch = %+v, want unchanged [one]", fetchResp.Data.Messages)
	}

	wAtLimit := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{
		"messages": chatSessionMessagesOfLen(500),
	})
	if wAtLimit.Code != http.StatusOK {
		t.Fatalf("500-message patch: status = %d, want 200, body=%s", wAtLimit.Code, wAtLimit.Body.String())
	}
}

// 11. PATCH persists a new model — pins updateChatSessionRequest.Model and
// the repo write, not just the wire shape.
func TestV2ChatSession_Update_PersistsModel(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "model swap", "model": "rt-alpha", "messages": []map[string]string{},
	})
	var createResp struct {
		Data struct {
			Id    int    `json:"id"`
			Model string `json:"model"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
	if createResp.Data.Model != "rt-alpha" {
		t.Fatalf("create model = %q, want rt-alpha", createResp.Data.Model)
	}
	id := createResp.Data.Id

	wPatch := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{"model": "rt-beta"})
	if wPatch.Code != http.StatusOK {
		t.Fatalf("patch model status = %d, want 200, body=%s", wPatch.Code, wPatch.Body.String())
	}
	var patchResp struct {
		Data struct {
			Model string `json:"model"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wPatch.Body.Bytes(), &patchResp); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if patchResp.Data.Model != "rt-beta" {
		t.Fatalf("patch response model = %q, want rt-beta", patchResp.Data.Model)
	}

	wFetch := ctx.do(http.MethodGet, "/"+strconv.Itoa(id), ctx.userID, nil)
	var fetchResp struct {
		Data struct {
			Model string `json:"model"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wFetch.Body.Bytes(), &fetchResp)
	if fetchResp.Data.Model != "rt-beta" {
		t.Fatalf("fetch after patch model = %q, want rt-beta (persisted)", fetchResp.Data.Model)
	}
}

// TestMaxChatSessionMessagesIsThe500TheCommentAdvertises pins the actual
// enforced value the same way TestMaxChatSessionsPerUserIsThe200... pins
// the session cap in the repo package: the 501/500 boundary tests below
// only prove "the boundary sits wherever the constant is", not that the
// constant itself is 500.
func TestMaxChatSessionMessagesIsThe500TheCommentAdvertises(t *testing.T) {
	if maxChatSessionMessages != 500 {
		t.Fatalf("maxChatSessionMessages = %d, want 500", maxChatSessionMessages)
	}
}

// 12. validateChatSessionModel rejects an over-length model on CREATE, not
// just on PATCH (cycle-11 repair round: this branch previously existed only
// on the PATCH path — a >128-char model on POST hit the varchar(128) column
// directly and surfaced as a 500 CHAT_SESSION_CREATE_FAILED instead of a
// 400). A model exactly at the 128-rune limit must still succeed (boundary
// is >, not >=).
func TestV2ChatSession_Create_RejectsOversizedModel(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	oversized := strings.Repeat("m", maxChatSessionModelRunes+1)
	w := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "oversized model", "model": oversized, "messages": []map[string]string{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized model create status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
	if code := chatSessionErrorCode(t, w); code != "INVALID_REQUEST" {
		t.Fatalf("oversized model create error_code = %q, want INVALID_REQUEST", code)
	}

	atLimit := strings.Repeat("n", maxChatSessionModelRunes)
	wOK := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "at-limit model", "model": atLimit, "messages": []map[string]string{},
	})
	if wOK.Code != http.StatusOK {
		t.Fatalf("at-limit model create status = %d, want 200, body=%s", wOK.Code, wOK.Body.String())
	}
}

// 13. Same rejection on PATCH, and a rejected PATCH must leave the stored
// model untouched (same "failed PATCH does not partially apply" contract
// as the title and message-list tests above).
func TestV2ChatSession_Update_RejectsOversizedModel(t *testing.T) {
	ctx := setupChatSessionRouter(t)

	wCreate := ctx.do(http.MethodPost, "", ctx.userID, map[string]any{
		"title": "starter", "model": "rt-alpha", "messages": []map[string]string{},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
	id := createResp.Data.Id

	oversized := strings.Repeat("p", maxChatSessionModelRunes+1)
	wPatch := ctx.do(http.MethodPatch, "/"+strconv.Itoa(id), ctx.userID, map[string]any{"model": oversized})
	if wPatch.Code != http.StatusBadRequest {
		t.Fatalf("oversized model patch status = %d, want 400, body=%s", wPatch.Code, wPatch.Body.String())
	}
	if code := chatSessionErrorCode(t, wPatch); code != "INVALID_REQUEST" {
		t.Fatalf("oversized model patch error_code = %q, want INVALID_REQUEST", code)
	}

	wFetch := ctx.do(http.MethodGet, "/"+strconv.Itoa(id), ctx.userID, nil)
	var fetchResp struct {
		Data struct {
			Model string `json:"model"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wFetch.Body.Bytes(), &fetchResp)
	if fetchResp.Data.Model != "rt-alpha" {
		t.Fatalf("model after rejected patch = %q, want unchanged %q", fetchResp.Data.Model, "rt-alpha")
	}
}
