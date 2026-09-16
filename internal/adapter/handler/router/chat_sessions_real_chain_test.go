package router

// chat_sessions_real_chain_test.go — REAL-CHAIN oracle for cycle-10 L3
// (console-completion-cycle10-2026-09-16.md, migration 038).
// internal/adapter/handler/v2_chat_session_test.go mounts the five
// GET/POST/PATCH/DELETE handlers directly on a bare gin.New() with
// "id"/"tenant_context" hand-seeded via c.Set — sound for the repo-wiring
// and validation claims it makes, but it does not prove the routes are
// reachable through the PRODUCTION UserAuth()+TenantSlugGuard() chain a
// real console request goes through (router/api-v2-router.go's tenantChat
// group). This file closes that gap the same way
// channel_sensitive_write_real_chain_test.go and
// l7_rankings_by_group_real_chain_test.go do: SetApiV2Router mounts the
// real middleware chain, and both identities come from seeded repo.User
// rows via the session, not a hand-set context key.
//
// Mutation target: the OWNERSHIP FAIL-CLOSED claim — dropping the
// "AND user_id = ?" predicate from any of GetChatSessionOwned/
// UpdateChatSessionOwned/DeleteChatSessionOwned (repo/chat_session.go)
// turns TestChatSessionsRealChain_OwnershipFailClosed_SameShape red: the
// other user's request against the owner's session id would start
// returning 200 instead of the same 404 the nonexistent id gets.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var chatSessionsRealChainDBCounter atomic.Int64

type chatSessionsRealChainFixture struct {
	tenantSlug  string
	userID      int
	otherUserID int
	serve       func(asUserID int, method, path string, body any) *httptest.ResponseRecorder
}

func setupChatSessionsRealChain(t *testing.T) *chatSessionsRealChainFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := chatSessionsRealChainDBCounter.Add(1)
	dbName := fmt.Sprintf("file:chatsessionsrealchain%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &entity.Tenant{}, &entity.ChatSession{}, &entity.ChatMessage{},
	} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false

	tenantID := fmt.Sprintf("chatrc-tenant-%d", n)
	tenantSlug := fmt.Sprintf("chatrc-slug-%d", n)
	tenant := &entity.Tenant{
		Id: tenantID, IDPOrgID: "chatrc-org-" + tenantID, Slug: tenantSlug,
		Name: "Chat RC Tenant", Status: entity.TenantStatusEnabled,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{
		Username: fmt.Sprintf("chatrc_user_%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("chatrc-user-%d@local", n), TenantId: tenantID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	other := &repo.User{
		Username: fmt.Sprintf("chatrc_other_%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("chatrc-other-%d@local", n), TenantId: tenantID,
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}

	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})

	byID := map[int]*repo.User{user.Id: user, other.Id: other}

	serve := func(asUserID int, method, path string, body any) *httptest.ResponseRecorder {
		u := byID[asUserID]
		engine := gin.New()
		engine.Use(gin.Recovery())
		store := cookie.NewStore([]byte("chat-sessions-real-chain-secret"))
		engine.Use(sessions.Sessions("session", store))
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", u.Username)
			s.Set("role", u.Role)
			s.Set("id", u.Id)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
		SetApiV2Router(engine)

		var reader *bytes.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	return &chatSessionsRealChainFixture{
		tenantSlug: tenantSlug, userID: user.Id, otherUserID: other.Id, serve: serve,
	}
}

// TestChatSessionsRealChain_RoundTrip proves, through the production route
// table (SetApiV2Router mounts GET/POST/PATCH/DELETE
// /api/v2/:tenant_slug/chat/sessions[/:id] under UserAuth()+
// TenantSlugGuard(), router/api-v2-router.go's tenantChat group), that a
// session created through one request is visible to list/fetch and gone
// after delete.
func TestChatSessionsRealChain_RoundTrip(t *testing.T) {
	fx := setupChatSessionsRealChain(t)
	base := "/api/v2/" + fx.tenantSlug + "/chat/sessions"

	wCreate := fx.serve(fx.userID, http.MethodPost, base, map[string]any{
		"title": "real chain trip",
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi there"},
		},
	})
	if wCreate.Code != http.StatusOK {
		t.Fatalf("POST %s: status = %d, want 200; body=%s", base, wCreate.Code, wCreate.Body.String())
	}
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create: %v; body=%s", err, wCreate.Body.String())
	}
	if createResp.Data.Id == 0 {
		t.Fatalf("create did not return an id; body=%s", wCreate.Body.String())
	}
	id := createResp.Data.Id
	itemPath := base + "/" + strconv.Itoa(id)

	wList := fx.serve(fx.userID, http.MethodGet, base, nil)
	if wList.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200; body=%s", base, wList.Code, wList.Body.String())
	}
	var listResp struct {
		Data struct {
			Sessions []struct {
				Id int `json:"id"`
			} `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	found := false
	for _, s := range listResp.Data.Sessions {
		if s.Id == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("list did not contain created session %d: %+v", id, listResp.Data.Sessions)
	}

	wFetch := fx.serve(fx.userID, http.MethodGet, itemPath, nil)
	if wFetch.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200; body=%s", itemPath, wFetch.Code, wFetch.Body.String())
	}

	wDelete := fx.serve(fx.userID, http.MethodDelete, itemPath, nil)
	if wDelete.Code != http.StatusOK {
		t.Fatalf("DELETE %s: status = %d, want 200; body=%s", itemPath, wDelete.Code, wDelete.Body.String())
	}

	wFetchAfterDelete := fx.serve(fx.userID, http.MethodGet, itemPath, nil)
	if wFetchAfterDelete.Code != http.StatusNotFound {
		t.Fatalf("GET %s after delete: status = %d, want 404; body=%s", itemPath, wFetchAfterDelete.Code, wFetchAfterDelete.Body.String())
	}
}

// TestChatSessionsRealChain_OwnershipFailClosed_SameShape is this lane's
// named IDOR oracle: through the real UserAuth()+TenantSlugGuard() chain,
// another user's session id and an id nobody ever created must answer the
// identical 404 (same status AND same body) — a caller must not be able to
// tell "exists but isn't yours" apart from "never existed".
func TestChatSessionsRealChain_OwnershipFailClosed_SameShape(t *testing.T) {
	fx := setupChatSessionsRealChain(t)
	base := "/api/v2/" + fx.tenantSlug + "/chat/sessions"

	wCreate := fx.serve(fx.userID, http.MethodPost, base, map[string]any{
		"title": "owner-only conversation", "model": "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "a secret"}},
	})
	var createResp struct {
		Data struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create: %v; body=%s", err, wCreate.Body.String())
	}
	ownedID := createResp.Data.Id
	nonexistentID := ownedID + 9000
	ownedPath := base + "/" + strconv.Itoa(ownedID)
	nonexistentPath := base + "/" + strconv.Itoa(nonexistentID)

	wOtherGet := fx.serve(fx.otherUserID, http.MethodGet, ownedPath, nil)
	wMissingGet := fx.serve(fx.otherUserID, http.MethodGet, nonexistentPath, nil)
	if wOtherGet.Code != http.StatusNotFound {
		t.Fatalf("GET %s as other user: status = %d, want 404; body=%s", ownedPath, wOtherGet.Code, wOtherGet.Body.String())
	}
	if wMissingGet.Code != http.StatusNotFound {
		t.Fatalf("GET %s: status = %d, want 404; body=%s", nonexistentPath, wMissingGet.Code, wMissingGet.Body.String())
	}
	if wOtherGet.Body.String() != wMissingGet.Body.String() {
		t.Fatalf("GET 404 bodies differ — leaks which case occurred:\n other-owner: %s\n nonexistent: %s",
			wOtherGet.Body.String(), wMissingGet.Body.String())
	}

	wOtherDelete := fx.serve(fx.otherUserID, http.MethodDelete, ownedPath, nil)
	wMissingDelete := fx.serve(fx.otherUserID, http.MethodDelete, nonexistentPath, nil)
	if wOtherDelete.Code != http.StatusNotFound || wMissingDelete.Code != http.StatusNotFound {
		t.Fatalf("DELETE statuses = %d/%d, want 404/404", wOtherDelete.Code, wMissingDelete.Code)
	}
	if wOtherDelete.Body.String() != wMissingDelete.Body.String() {
		t.Fatalf("DELETE 404 bodies differ:\n other-owner: %s\n nonexistent: %s",
			wOtherDelete.Body.String(), wMissingDelete.Body.String())
	}

	wOtherPatch := fx.serve(fx.otherUserID, http.MethodPatch, ownedPath, map[string]any{"title": "hijacked"})
	wMissingPatch := fx.serve(fx.otherUserID, http.MethodPatch, nonexistentPath, map[string]any{"title": "hijacked"})
	if wOtherPatch.Code != http.StatusNotFound || wMissingPatch.Code != http.StatusNotFound {
		t.Fatalf("PATCH statuses = %d/%d, want 404/404", wOtherPatch.Code, wMissingPatch.Code)
	}
	if wOtherPatch.Body.String() != wMissingPatch.Body.String() {
		t.Fatalf("PATCH 404 bodies differ:\n other-owner: %s\n nonexistent: %s",
			wOtherPatch.Body.String(), wMissingPatch.Body.String())
	}

	// Confirm the session genuinely still belongs to, and is intact for,
	// its real owner — the 404s above were ownership, not data loss, and
	// the other user's PATCH attempt must not have applied.
	wOwnerFetch := fx.serve(fx.userID, http.MethodGet, ownedPath, nil)
	if wOwnerFetch.Code != http.StatusOK {
		t.Fatalf("owner GET %s: status = %d, want 200; body=%s", ownedPath, wOwnerFetch.Code, wOwnerFetch.Body.String())
	}
	var ownerResp struct {
		Data struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wOwnerFetch.Body.Bytes(), &ownerResp); err != nil {
		t.Fatalf("unmarshal owner fetch: %v", err)
	}
	if ownerResp.Data.Title != "owner-only conversation" {
		t.Fatalf("title = %q, want unchanged %q (other user's PATCH must not have applied)",
			ownerResp.Data.Title, "owner-only conversation")
	}
}
