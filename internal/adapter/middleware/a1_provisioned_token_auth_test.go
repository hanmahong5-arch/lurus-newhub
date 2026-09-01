package middleware

// a1_provisioned_token_auth_test.go — TokenAuth must admit a PROVISIONED key.
//
// Provisioned keys (handler/provisioning.go) are tenant-scoped and are minted
// with UserId=0 on purpose; no user row is ever created for them. Before the A1
// fix TokenAuth called repo.GetUserCache(0) unconditionally, which falls through
// to repo.GetUserById(0) → hard error "id 为空！", so every relay call on a
// provisioned key answered 500 — the feature was structurally dead on the relay
// path. These tests pin the admitted state (tenant, using-group, project id) and
// the guarantee that a user-owned token's own gates did not get loosened.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var provisionedTestDBCounter atomic.Int64

// capturedAuthState is what the route handler observes AFTER TokenAuth ran —
// i.e. the contract downstream relay code (Distribute, relay_info.GenRelayInfo,
// PoolBalanceCheck) reads out of the gin context.
type capturedAuthState struct {
	tenantID     string
	usingGroup   string
	userGroupCtx string
	projectID    int
	userID       int
	username     string
	hasTenant    bool
	tenantCtx    *TenantContext
}

// setupProvisionedTestRouter builds an isolated sqlite-backed token row and
// mounts the real TokenAuth() in front of a handler that snapshots the context.
func setupProvisionedTestRouter(t *testing.T, tok *repo.Token, tenant *entity.Tenant) (*gin.Engine, *capturedAuthState, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:provisioned_test_%d?mode=memory&cache=shared", provisionedTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Tenant{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			// SQLite has a single global index namespace; composite indexes
			// shared across models produce harmless "already exists" errors.
			if !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("migrate %T: %v", tbl, err)
			}
		}
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	if tenant != nil {
		if err := db.Create(tenant).Error; err != nil {
			t.Fatalf("create tenant: %v", err)
		}
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	got := &capturedAuthState{}
	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/v1/chat/completions", TokenAuth(), func(c *gin.Context) {
		got.tenantID = c.GetString("tenant_id")
		_, got.hasTenant = c.Get("tenant_id")
		got.usingGroup = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		got.userGroupCtx = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		got.projectID = common.GetContextKeyInt(c, constant.ContextKeyProjectId)
		got.userID = common.GetContextKeyInt(c, constant.ContextKeyUserId)
		got.username = common.GetContextKeyString(c, constant.ContextKeyUserName)
		if v, ok := c.Get("tenant_context"); ok {
			got.tenantCtx, _ = v.(*TenantContext)
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	cleanup := func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return r, got, cleanup
}

func provisionedProbe(t *testing.T, r *gin.Engine, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestTokenAuth_ProvisionedKey_UserIdZero_Admitted is the headline A1 lock: a
// UserId=0 token must relay, not 500. Mutation oracle: restoring the
// unconditional repo.GetUserCache(token.UserId) turns this 200 into a 500
// carrying "id 为空！".
func TestTokenAuth_ProvisionedKey_UserIdZero_Admitted(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         0, // provisioned: tenant-scoped, no user row
		TenantId:       "t-prov",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "provisioned-key",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    500_000,
		UnlimitedQuota: false,
		Group:          "vip",
		ProjectId:      77,
	}
	tenant := &entity.Tenant{
		Id:       "t-prov",
		IDPOrgID: "org-prov",
		Slug:     "prov",
		Name:     "Provisioned Tenant",
		Status:   entity.TenantStatusEnabled,
	}
	r, got, cleanup := setupProvisionedTestRouter(t, tok, tenant)
	defer cleanup()

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (provisioned UserId=0 key must relay); body=%s",
			w.Code, w.Body.String())
	}

	// Tenant gates + tenant context injection must still have run: the tenant
	// is what the pool debit and PoolBalanceCheck key on.
	if !got.hasTenant || got.tenantID != "t-prov" {
		t.Errorf("tenant_id = %q (present=%v), want %q", got.tenantID, got.hasTenant, "t-prov")
	}
	if got.tenantCtx == nil {
		t.Fatal("tenant_context missing — PoolBalanceCheck / v2 handlers read it")
	}
	if got.tenantCtx.TenantID != "t-prov" || got.tenantCtx.UserID != 0 {
		t.Errorf("tenant_context = {%q, %d}, want {t-prov, 0}", got.tenantCtx.TenantID, got.tenantCtx.UserID)
	}
	// A tenant-scoped key has no user row, so no email/username to carry.
	if got.tenantCtx.Email != "" || got.tenantCtx.Username != "" {
		t.Errorf("tenant_context identity = {%q, %q}, want both empty for a provisioned key",
			got.tenantCtx.Email, got.tenantCtx.Username)
	}
	// The token's own group is the authority (no user group to validate against).
	if got.usingGroup != "vip" {
		t.Errorf("using-group = %q, want %q (token group)", got.usingGroup, "vip")
	}
	// Project attribution must not be conditional on having a user.
	if got.projectID != 77 {
		t.Errorf("project_id = %d, want 77", got.projectID)
	}
	if got.userID != 0 {
		t.Errorf("context id = %d, want 0", got.userID)
	}
	if got.username != "" {
		t.Errorf("username = %q, want empty (accepted: provisioned traffic logs no username)", got.username)
	}
}

// TestTokenAuth_ProvisionedKey_EmptyGroup_FallsBackToDefault pins the documented
// fallback: a provisioned token with no group relays as "default", the same
// default handler/provisioning.go stamps when the caller omits one.
func TestTokenAuth_ProvisionedKey_EmptyGroup_FallsBackToDefault(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         0,
		TenantId:       "t-prov-empty",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "provisioned-nogroup",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "",
	}
	r, got, cleanup := setupProvisionedTestRouter(t, tok, nil)
	defer cleanup()

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got.usingGroup != "default" {
		t.Errorf("using-group = %q, want %q — an empty using-group makes channel selection unanswerable",
			got.usingGroup, "default")
	}
}

// TestTokenAuth_ProvisionedKey_DisabledTenant_Rejected proves the tenant gate is
// still armed for provisioned keys: making UserId=0 a first-class principal must
// not become a way to bypass tenant suspension.
func TestTokenAuth_ProvisionedKey_DisabledTenant_Rejected(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         0,
		TenantId:       "t-prov-off",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "provisioned-suspended",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	tenant := &entity.Tenant{
		Id:       "t-prov-off",
		IDPOrgID: "org-prov-off",
		Slug:     "prov-off",
		Name:     "Suspended Tenant",
		Status:   entity.TenantStatusSuspended,
	}
	r, _, cleanup := setupProvisionedTestRouter(t, tok, tenant)
	defer cleanup()

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (suspended tenant must lock out its provisioned keys); body=%s",
			w.Code, w.Body.String())
	}
}

// TestTokenAuth_ProvisionedKey_ExhaustedTokenQuota_Rejected proves the token's
// own spending cap is still the gate for a key that has no user balance behind
// it: with no user leg, this 402 is the ONLY thing standing between a
// provisioned key and unmetered relay.
func TestTokenAuth_ProvisionedKey_ExhaustedTokenQuota_Rejected(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         0,
		TenantId:       "t-prov-broke",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "provisioned-broke",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    0,
		UnlimitedQuota: false,
		Group:          "default",
	}
	r, _, cleanup := setupProvisionedTestRouter(t, tok, nil)
	defer cleanup()

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402 (token cap is the only money gate a provisioned key has); body=%s",
			w.Code, w.Body.String())
	}
}

// TestTokenAuth_UserOwnedToken_DisabledUser_StillRejected is the anti-regression
// half: the A1 fix must skip the user-status gate ONLY for UserId==0. A banned
// user's token must still be refused.
func TestTokenAuth_UserOwnedToken_DisabledUser_StillRejected(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         0, // replaced below with the real user id
		TenantId:       "default",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "banned-owner",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "",
	}
	// The user row must exist before the token references it, and
	// setupProvisionedTestRouter creates the token — so seed the user through a
	// pre-created router, then patch the token's owner.
	r, _, cleanup := setupProvisionedTestRouter(t, tok, nil)
	defer cleanup()

	user := &repo.User{
		Username:    "banneduser",
		DisplayName: "Banned",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusDisabled,
		Email:       "banned@local",
		TenantId:    "default",
		Quota:       1_000_000,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repo.DB.Model(&repo.Token{}).Where("key = ?", key).
		Update("user_id", user.Id).Error; err != nil {
		t.Fatalf("bind token to user: %v", err)
	}

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (a banned user's token must stay rejected); body=%s",
			w.Code, w.Body.String())
	}
}

// TestTokenAuth_ProvisionedKey_SeedsUserGroupContext: UserBaseWriteContext is
// skipped for provisioned keys, so TokenAuth must seed ContextKeyUserGroup
// itself — the distributor's and channel selection's "auto"-group resolution
// (app.GetUserAutoGroup) read it, and an empty value there would resolve an
// auto-group token against a nonexistent owner group.
func TestTokenAuth_ProvisionedKey_SeedsUserGroupContext(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:       0,
		TenantId:     "t-prov-ug",
		Key:          key,
		Status:       common.TokenStatusEnabled,
		Name:         "provisioned-usergroup",
		CreatedTime:  common.GetTimestamp(),
		AccessedTime: common.GetTimestamp(),
		ExpiredTime:  -1,
		RemainQuota:  500_000,
		Group:        "vip",
	}
	tenant := &entity.Tenant{
		Id:       "t-prov-ug",
		IDPOrgID: "org-prov-ug",
		Slug:     "prov-ug",
		Name:     "Provisioned UG Tenant",
		Status:   entity.TenantStatusEnabled,
	}
	r, got, cleanup := setupProvisionedTestRouter(t, tok, tenant)
	defer cleanup()

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got.userGroupCtx != "vip" {
		t.Errorf("ContextKeyUserGroup = %q, want %q (auto-group resolution input)", got.userGroupCtx, "vip")
	}
}

// TestTokenAuth_UserIdZeroEmptyTenant_FailsClosed: the provisioned branch
// requires a non-empty TenantId as a positive marker (the provisioning handler
// always stamps one). A stray row with user_id=0 AND an empty tenant must NOT
// inherit the relaxed user gates — it keeps the pre-A1 fail-closed 500 instead
// of relaying as tenant "default" with no user-status check.
func TestTokenAuth_UserIdZeroEmptyTenant_FailsClosed(t *testing.T) {
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:       0,
		TenantId:     "", // stray/legacy shape — NOT a provisioned mint
		Key:          key,
		Status:       common.TokenStatusEnabled,
		Name:         "stray-user0-key",
		CreatedTime:  common.GetTimestamp(),
		AccessedTime: common.GetTimestamp(),
		ExpiredTime:  -1,
		RemainQuota:  500_000,
	}
	r, _, cleanup := setupProvisionedTestRouter(t, tok, nil)
	defer cleanup()

	// GORM's column default rewrites an empty TenantId to 'default' on Create,
	// so the stray shape can only exist via raw SQL — exactly how a pre-tenancy
	// row would look in an old database.
	if err := repo.DB.Exec("UPDATE tokens SET tenant_id = '' WHERE key = ?", key).Error; err != nil {
		t.Fatalf("force empty tenant_id: %v", err)
	}

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (fail closed for user_id=0 with empty tenant); body=%s",
			w.Code, w.Body.String())
	}
}
