package handler

// playground_token_identity_test.go — 锁住 /pg 计费半边:Playground 在把请求交给
// Relay 之前会再调一次 SetupContextForToken,如果那里传的是零值 token,就会把
// PlaygroundAuth 刚解析出来的真实身份(token_id / token_key / project_id)整片
// 覆盖成 0。后果不在鉴权而在钱:PostConsumeQuota 的租户信用池扣费口
// (`relayInfo.TokenId > 0`)从此永远不成立,playground 花掉的额度不扣任何池子,
// 产生的日志行也永远缺 project 归集。
//
// 这条锁是行为锁而不是形状锁:它驱动真实 handler,断言 handler 返回后上下文里
// 的身份仍是进入时那一份。把 tempToken 改回零值字面量即变红。

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

// TestPlayground_PreservesResolvedTokenIdentity 断言 Playground 不会清掉
// PlaygroundAuth 已解析的令牌身份与 project 归集。数值全部取自上下文,
// 与 Relay 之后的成败无关(Relay 在本用例里必然因请求体不合法而早退)。
func TestPlayground_PreservesResolvedTokenIdentity(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	const (
		ownerID   = 8801
		tokenID   = 7701
		tokenKey  = "pg-identity-lock-key"
		projectID = 4242
	)

	owner := repo.User{
		Id:       ownerID,
		Username: "pg-identity-owner",
		TenantId: "tenant-pg",
		Group:    "default",
		Status:   common.UserStatusEnabled,
		Quota:    1000,
	}
	if err := repo.DB.Create(&owner).Error; err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	// 进入 handler 前的状态 = PlaygroundAuth 解析出来的真实身份。
	c.Set(string(constant.ContextKeyUserId), ownerID)
	c.Set(string(constant.ContextKeyTokenId), tokenID)
	c.Set(string(constant.ContextKeyTokenKey), tokenKey)
	common.SetContextKey(c, constant.ContextKeyProjectId, projectID)

	Playground(c)

	if got := common.GetContextKeyInt(c, constant.ContextKeyTokenId); got != tokenID {
		t.Fatalf("token_id = %d, want %d(被零值 token 覆盖后租户信用池永不扣费)", got, tokenID)
	}
	if got := common.GetContextKeyString(c, constant.ContextKeyTokenKey); got != tokenKey {
		t.Errorf("token_key = %q, want %q", got, tokenKey)
	}
	if got := common.GetContextKeyInt(c, constant.ContextKeyProjectId); got != projectID {
		t.Errorf("project_id = %d, want %d(成本归集丢失后日志行永久不可归因)", got, projectID)
	}
}
