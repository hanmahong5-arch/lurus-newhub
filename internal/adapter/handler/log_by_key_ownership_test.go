package handler

// log_by_key_ownership_test.go — end-to-end guard for GET /api/log/token.
//
// The route is TokenAuth-gated, but the key it reports on is a query
// parameter, so the handler must derive ownership from the authenticated
// principal (context "id" + "tenant_id") and never from the key. These tests
// mount the handler behind a stand-in for TokenAuth and check what a caller
// actually receives for someone else's key.

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// asCaller stands in for TokenAuth: it sets exactly the two context keys the
// real middleware sets that ownership decisions depend on.
func asCaller(userID int, tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("id", userID)
		c.Set("tenant_id", tenantID)
		c.Next()
	}
}

// seedOtherUserToken creates a second user in the same tenant holding a token
// with one log row, i.e. the data a stranger's key would expose.
func seedOtherUserToken(t *testing.T, ctx *V2TestContext, username string) *repo.Token {
	t.Helper()
	other := &repo.User{
		Username: username,
		Email:    username + "@test.local",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		TenantId: ctx.TenantID,
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	tok := SeedV2Token(t, ctx, other.Id, username+"-token")
	if err := ctx.DB.Create(&repo.Log{
		TenantId: ctx.TenantID, UserId: other.Id, TokenId: tok.Id, Type: repo.LogTypeConsume,
		Content: "other user consume", ModelName: "gpt-4o-mini", Quota: 500, CreatedAt: common.GetTimestamp(),
	}).Error; err != nil {
		t.Fatalf("seed other user log: %v", err)
	}
	return tok
}

// TestGetLogByKeyHandler_ForeignKeyReturnsNothing is the regression this whole
// change exists for: before the ownership check, any authenticated caller who
// held (or guessed) another account's key got that account's full log history
// back through this endpoint.
func TestGetLogByKeyHandler_ForeignKeyReturnsNothing(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	foreign := seedOtherUserToken(t, ctx, "logbykey-stranger")

	r := gin.New()
	r.GET("/api/log/token", asCaller(ctx.NormalUser.Id, ctx.TenantID), GetLogByKey)

	w := doAnalyticsGet(r, "/api/log/token?key=sk-"+foreign.Key)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected a data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 0 {
		t.Fatalf("caller %d received %d log rows belonging to another account (body %s)", ctx.NormalUser.Id, len(items), w.Body.String())
	}
}

// TestGetLogByKeyHandler_OwnKeyStillWorks keeps the legitimate flow honest:
// the guard must not turn the endpoint into a permanent empty page.
func TestGetLogByKeyHandler_OwnKeyStillWorks(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "logbykey-own-token")
	if err := ctx.DB.Create(&repo.Log{
		TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: tok.Id, Type: repo.LogTypeConsume,
		Content: "own consume", ModelName: "gpt-4o", Quota: 42, CreatedAt: common.GetTimestamp(),
	}).Error; err != nil {
		t.Fatalf("seed own log: %v", err)
	}

	r := gin.New()
	r.GET("/api/log/token", asCaller(ctx.NormalUser.Id, ctx.TenantID), GetLogByKey)

	w := doAnalyticsGet(r, "/api/log/token?key=sk-"+tok.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("own key should return exactly 1 row, got %v (body %s)", resp["data"], w.Body.String())
	}
}

// TestGetLogByKeyHandler_UnauthenticatedContextDenied covers the shape a
// caller with no resolvable principal gets — context id 0 is also what a
// provisioned token yields, and 0 must never match another id-0 token.
func TestGetLogByKeyHandler_UnauthenticatedContextDenied(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "logbykey-noauth-token")
	if err := ctx.DB.Create(&repo.Log{
		TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: tok.Id, Type: repo.LogTypeConsume,
		Content: "own consume", ModelName: "gpt-4o", Quota: 7, CreatedAt: common.GetTimestamp(),
	}).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	r := gin.New()
	r.GET("/api/log/token", GetLogByKey) // no principal in context at all

	w := doAnalyticsGet(r, "/api/log/token?key=sk-"+tok.Key)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok || len(items) != 0 {
		t.Fatalf("a request with no principal must resolve nothing, got %v (body %s)", resp["data"], w.Body.String())
	}
}
