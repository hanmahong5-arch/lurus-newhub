package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// fixBackfillIdentityStub serves the platform account lookup used by the
// backfill handler, always answering with the supplied account id.
func fixBackfillIdentityStub(t *testing.T, accountID int64) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%d,"lurus_id":"lu_test","idp_subject":"fix-backfill-sub","email":"backfill@test.local","status":1}`, accountID)
	}))
	t.Cleanup(srv.Close)

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })
}

// fixBackfillSeed creates one un-backfilled token plus an active identity
// mapping for the given user and returns the token id.
func fixBackfillSeed(t *testing.T, ctx *V2TestContext, userID int, tokenKey string) int {
	t.Helper()
	token := &repo.Token{
		UserId:            userID,
		TenantId:          ctx.TenantID,
		Key:               tokenKey,
		Name:              "fix-backfill-token",
		Status:            1,
		IdentityAccountID: 0,
	}
	if err := ctx.DB.Create(token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	mapping := &repo.UserIdentityMapping{
		LurusUserID: userID,
		IDPSubject:  "fix-backfill-sub",
		TenantID:    ctx.TenantID,
		Email:       "backfill@test.local",
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := ctx.DB.Create(mapping).Error; err != nil {
		t.Fatalf("seed identity mapping: %v", err)
	}
	return token.Id
}

func fixBackfillCall(t *testing.T) map[string]interface{} {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/internal/admin/backfill-token-accounts", InternalBackfillTokenAccountIDs)

	req := httptest.NewRequest(http.MethodPost, "/internal/admin/backfill-token-accounts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	return ParseV2Response(t, w)
}

// 回归：平台返回退化账号（id<=0）时不能计入 users_matched——写入循环本来就
// 拒绝 id<=0，计数器若照样 +1，运维看到的 users_matched 与 tokens_updated 会失真。
func TestFixBackfillZeroAccountIDNotCountedAsMatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	fixBackfillIdentityStub(t, 0)
	tokenID := fixBackfillSeed(t, ctx, ctx.NormalUser.Id, "fix-backfill-key-zero-0000000000000000000000")

	resp := fixBackfillCall(t)

	if matched := int(resp["users_matched"].(float64)); matched != 0 {
		t.Fatalf("expected users_matched=0 for a degenerate account id, got %d (body %v)", matched, resp)
	}
	if updated := int(resp["tokens_updated"].(float64)); updated != 0 {
		t.Fatalf("expected tokens_updated=0, got %d", updated)
	}
	var stored repo.Token
	if err := ctx.DB.First(&stored, tokenID).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if stored.IdentityAccountID != 0 {
		t.Fatalf("token must stay un-backfilled, got identity_account_id=%d", stored.IdentityAccountID)
	}
}

// 正常账号仍须被计入并写回，确保守卫没有把有效回填一起挡掉。
func TestFixBackfillPositiveAccountIDStillApplied(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	fixBackfillIdentityStub(t, 4242)
	tokenID := fixBackfillSeed(t, ctx, ctx.NormalUser.Id, "fix-backfill-key-positive-000000000000000000")

	resp := fixBackfillCall(t)

	if matched := int(resp["users_matched"].(float64)); matched != 1 {
		t.Fatalf("expected users_matched=1, got %d (body %v)", matched, resp)
	}
	if updated := int(resp["tokens_updated"].(float64)); updated != 1 {
		t.Fatalf("expected tokens_updated=1, got %d (body %v)", updated, resp)
	}
	var stored repo.Token
	if err := ctx.DB.First(&stored, tokenID).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if stored.IdentityAccountID != 4242 {
		t.Fatalf("expected identity_account_id=4242, got %d", stored.IdentityAccountID)
	}
}
