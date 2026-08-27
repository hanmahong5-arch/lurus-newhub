package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var switchUserTopupDBCounter atomic.Int64

type switchUserTopupTestCtx struct {
	router *gin.Engine
	db     *gorm.DB
	user   *repo.User
}

// setupSwitchUserTopupTest mirrors setupSwitchUserInfoTest's harness shape
// (in-memory SQLite, repo.DB/LOG_DB swap, t.Cleanup restore) plus the two
// extra tables SwitchUserTopup's call into repo.Redeem touches: Redemption
// (the code being redeemed) and Log (repo.Redeem's audit trail write).
func setupSwitchUserTopupTest(t *testing.T) *switchUserTopupTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:sutuptest%d?mode=memory&cache=shared", switchUserTopupDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Redemption{}, &repo.Log{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			if err := sqlDB.Close(); err != nil {
				t.Errorf("close sqlite: %v", err)
			}
		}
	})

	user := &repo.User{
		Username:    fmt.Sprintf("sutupuser%d", switchUserTopupDBCounter.Load()),
		DisplayName: "SUTUP User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "sutupuser@test.local",
		Group:       "default",
		Quota:       500_000,
		UsedQuota:   120_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	router := gin.New()
	router.POST("/api/v2/switch/user/topup", SwitchUserTopup)

	return &switchUserTopupTestCtx{router: router, db: db, user: user}
}

func (s *switchUserTopupTestCtx) seedToken(t *testing.T, override repo.Token) *repo.Token {
	t.Helper()
	tok := repo.Token{
		UserId:         s.user.Id,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "sutup-test-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    200_000,
		UnlimitedQuota: false,
		Group:          "default",
	}
	if override.Status != 0 {
		tok.Status = override.Status
	}
	if err := s.db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return &tok
}

// seedRedemption inserts a redemption code directly (bypassing
// repo.RedemptionInsert's tenant scoping, which isn't wired in this
// harness).
//
// Comment corrected 2026-08-27: this used to say TenantId "default" passes
// repo.Redeem's tenant check because of a "v1-compat bypass" for
// "default"-tenant codes. That bypass has been removed (G5a — see
// repo/redemption.go's Redeem, whose tenant equality check is now
// unconditional). The reason these fixtures still pass is plainer: the
// seeded user has no explicit TenantId, so entity.User's GORM
// `default:'default'` column default gives it "default" too, and the check
// is an equality that both sides satisfy. Seed a user with a different
// TenantId and this same helper's codes will now be rejected — which is
// exactly what TestSwitchUserTopup_CrossTenantCodeRejected below pins.
func (s *switchUserTopupTestCtx) seedRedemption(t *testing.T, key string, quota int) *repo.Redemption {
	t.Helper()
	r := &repo.Redemption{
		TenantId:    "default",
		Key:         key,
		Name:        "sutup-test-code",
		Quota:       quota,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := s.db.Create(r).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}
	return r
}

func (s *switchUserTopupTestCtx) post(t *testing.T, authorization string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	switch v := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case []byte:
		reader = bytes.NewReader(v)
	default:
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/switch/user/topup", reader)
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func decodeTopupEnvelope(t *testing.T, w *httptest.ResponseRecorder) (success bool, message string, quota int64) {
	t.Helper()
	var envelope struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Quota int64 `json:"quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v, raw: %s", err, w.Body.String())
	}
	return envelope.Success, envelope.Message, envelope.Data.Quota
}

// ---------------------------------------------------------------------------
// Success: quota lands on the token owner's account and the code flips to
// used.
// ---------------------------------------------------------------------------

func TestSwitchUserTopup_Success(t *testing.T) {
	ctx := setupSwitchUserTopupTest(t)
	tok := ctx.seedToken(t, repo.Token{})
	code := common.GetRandomString(32)
	ctx.seedRedemption(t, code, 300_000)

	w := ctx.post(t, "Bearer sk-"+tok.Key, map[string]string{"key": code})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	success, _, quota := decodeTopupEnvelope(t, w)
	if !success {
		t.Fatalf("success = false, want true; body: %s", w.Body.String())
	}
	if quota != 300_000 {
		t.Errorf("data.quota = %d, want 300000", quota)
	}

	// The user's balance must reflect the credited quota.
	var refreshedUser repo.User
	if err := ctx.db.Where("id = ?", ctx.user.Id).First(&refreshedUser).Error; err != nil {
		t.Fatalf("refetch user: %v", err)
	}
	if refreshedUser.Quota != 500_000+300_000 {
		t.Errorf("user.Quota after topup = %d, want %d", refreshedUser.Quota, 500_000+300_000)
	}

	// The redemption row must be marked used.
	var refreshedRedemption repo.Redemption
	if err := repo.WithoutTenantIsolation(ctx.db).Where(`"key" = ?`, code).First(&refreshedRedemption).Error; err != nil {
		t.Fatalf("refetch redemption: %v", err)
	}
	if refreshedRedemption.Status != common.RedemptionCodeStatusUsed {
		t.Errorf("redemption.Status = %d, want RedemptionCodeStatusUsed (%d)", refreshedRedemption.Status, common.RedemptionCodeStatusUsed)
	}
	if refreshedRedemption.UsedUserId != ctx.user.Id {
		t.Errorf("redemption.UsedUserId = %d, want %d", refreshedRedemption.UsedUserId, ctx.user.Id)
	}
}

// ---------------------------------------------------------------------------
// Repeat redemption of the same code must fail with 400, and must not
// double-credit the account.
// ---------------------------------------------------------------------------

func TestSwitchUserTopup_RepeatSameCode(t *testing.T) {
	ctx := setupSwitchUserTopupTest(t)
	tok := ctx.seedToken(t, repo.Token{})
	code := common.GetRandomString(32)
	ctx.seedRedemption(t, code, 150_000)

	first := ctx.post(t, tok.Key, map[string]string{"key": code})
	if first.Code != http.StatusOK {
		t.Fatalf("first redeem: status = %d, want 200; body: %s", first.Code, first.Body.String())
	}
	if success, _, _ := decodeTopupEnvelope(t, first); !success {
		t.Fatalf("first redeem: success = false, want true; body: %s", first.Body.String())
	}

	second := ctx.post(t, tok.Key, map[string]string{"key": code})
	if second.Code != http.StatusBadRequest {
		t.Fatalf("second redeem: status = %d, want 400; body: %s", second.Code, second.Body.String())
	}
	success, message, _ := decodeTopupEnvelope(t, second)
	if success {
		t.Fatalf("second redeem: success = true, want false")
	}
	if message == "" {
		t.Errorf("second redeem: expected non-empty message")
	}

	// Balance must reflect exactly one credit, not two.
	var refreshedUser repo.User
	if err := ctx.db.Where("id = ?", ctx.user.Id).First(&refreshedUser).Error; err != nil {
		t.Fatalf("refetch user: %v", err)
	}
	if refreshedUser.Quota != 500_000+150_000 {
		t.Errorf("user.Quota after repeat = %d, want %d (single credit)", refreshedUser.Quota, 500_000+150_000)
	}
}

// ---------------------------------------------------------------------------
// Non-existent code: 400.
// ---------------------------------------------------------------------------

func TestSwitchUserTopup_UnknownCode(t *testing.T) {
	ctx := setupSwitchUserTopupTest(t)
	tok := ctx.seedToken(t, repo.Token{})

	w := ctx.post(t, tok.Key, map[string]string{"key": "doesnotexist00000000000000000000"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	success, message, _ := decodeTopupEnvelope(t, w)
	if success {
		t.Fatalf("success = true, want false")
	}
	if message == "" {
		t.Errorf("expected non-empty message")
	}
}

// ---------------------------------------------------------------------------
// Cross-tenant rejection (G5a). repo.Redeem's tenant equality check used to
// be skipped whenever the code's TenantId was "default", so a
// "default"-tenant code was redeemable by any user of any tenant. It is now
// unconditional, and THIS route is one of the two callers that has no
// pre-gate and no escape-hatch env var — SWITCH_REDEEM_ALLOW_DEFAULT_TENANT
// only guards SwitchRedeemAnonymous. Nothing in the tree observed this
// route's behavior change until this test.
//
// Note what "cross-tenant" means for the currently-shipping mint path: the
// v1 admin console stamps every code it creates with TenantId "default"
// (handler/redemption.go's AddRedemption falls back to "default" because
// nothing in the /api/redemption chain sets a tenant_id context key), so
// this is not an exotic case — it is what happens to any tenant user who
// tries to redeem an admin-minted code.
// ---------------------------------------------------------------------------

func TestSwitchUserTopup_CrossTenantCodeRejected(t *testing.T) {
	ctx := setupSwitchUserTopupTest(t)
	tok := ctx.seedToken(t, repo.Token{})

	// Move the token owner out of the "default" tenant. The code seeded below
	// stays in "default" (seedRedemption's fixture, matching what the v1 admin
	// console mints), so the two no longer match.
	if err := ctx.db.Model(&repo.User{}).Where("id = ?", ctx.user.Id).
		Update("tenant_id", "acme-reseller").Error; err != nil {
		t.Fatalf("move user to another tenant: %v", err)
	}

	code := common.GetRandomString(32)
	red := ctx.seedRedemption(t, code, 300_000)

	w := ctx.post(t, "Bearer sk-"+tok.Key, map[string]string{"key": code})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (repo.Redeem must refuse a code from another tenant); body: %s", w.Code, w.Body.String())
	}
	success, message, quota := decodeTopupEnvelope(t, w)
	if success {
		t.Fatalf("success = true, want false — a 'default'-tenant code was credited to a user in tenant %q", "acme-reseller")
	}
	if !strings.Contains(message, "不属于当前租户") {
		t.Errorf("message = %q, want the tenant-mismatch sentinel — a different rejection reason means the request failed before reaching the tenant check, which would make this test pass without observing it", message)
	}
	if quota != 0 {
		t.Errorf("data.quota = %d, want 0", quota)
	}

	// The money half: no balance moved and the code is still spendable by its
	// rightful tenant.
	var refreshedUser repo.User
	if err := ctx.db.Where("id = ?", ctx.user.Id).First(&refreshedUser).Error; err != nil {
		t.Fatalf("refetch user: %v", err)
	}
	if refreshedUser.Quota != 500_000 {
		t.Errorf("user quota = %d, want 500000 unchanged", refreshedUser.Quota)
	}
	var refreshedCode repo.Redemption
	if err := ctx.db.Where("id = ?", red.Id).First(&refreshedCode).Error; err != nil {
		t.Fatalf("refetch redemption: %v", err)
	}
	if refreshedCode.Status != common.RedemptionCodeStatusEnabled {
		t.Errorf("redemption status = %d, want %d (a rejected attempt must not consume the code)", refreshedCode.Status, common.RedemptionCodeStatusEnabled)
	}
}

// ---------------------------------------------------------------------------
// Auth failures: no header / unknown token / disabled token must all 401,
// and must never reach repo.Redeem (no accidental quota mutation).
// ---------------------------------------------------------------------------

func TestSwitchUserTopup_AuthFailures(t *testing.T) {
	ctx := setupSwitchUserTopupTest(t)
	disabled := ctx.seedToken(t, repo.Token{Status: common.TokenStatusDisabled})
	code := common.GetRandomString(32)
	ctx.seedRedemption(t, code, 50_000)

	cases := []struct {
		name string
		auth string
	}{
		{"missing header", ""},
		{"unknown token", "sk-doesnotexist0000000000000000000000"},
		{"disabled token", disabled.Key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := ctx.post(t, tc.auth, map[string]string{"key": code})
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
			}
		})
	}

	// The code must remain unredeemed — none of the auth failures above
	// should have reached repo.Redeem.
	var refreshedRedemption repo.Redemption
	if err := repo.WithoutTenantIsolation(ctx.db).Where(`"key" = ?`, code).First(&refreshedRedemption).Error; err != nil {
		t.Fatalf("refetch redemption: %v", err)
	}
	if refreshedRedemption.Status != common.RedemptionCodeStatusEnabled {
		t.Errorf("redemption.Status = %d, want still-enabled (%d) after auth failures", refreshedRedemption.Status, common.RedemptionCodeStatusEnabled)
	}
}

// ---------------------------------------------------------------------------
// Malformed body: 400, even with a valid token.
// ---------------------------------------------------------------------------

func TestSwitchUserTopup_BadBody(t *testing.T) {
	ctx := setupSwitchUserTopupTest(t)
	tok := ctx.seedToken(t, repo.Token{})

	cases := []struct {
		name string
		body interface{}
	}{
		{"not json", []byte("not-a-json-object")},
		{"empty key", map[string]string{"key": ""}},
		{"whitespace key", map[string]string{"key": "   "}},
		{"missing key field", map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := ctx.post(t, tok.Key, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			success, _, _ := decodeTopupEnvelope(t, w)
			if success {
				t.Errorf("success = true, want false")
			}
		})
	}
}
