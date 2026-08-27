package middleware

// l3_token_quota_402_test.go — D4 (lane L3, corrected by lane R2/B2):
// TokenAuth must answer a quota-exhausted token with HTTP 402 (not 401), so
// an OpenAI-compatible SDK does not classify quota exhaustion as an auth
// failure and start rotating keys instead of prompting a fix (live
// 2026-08-26 repro: remain_quota=0 token got a 401).
//
// R2 correction (B2): the 402's guidance must NOT be a wallet topup_url.
// ErrTokenQuotaExhausted is a per-TOKEN spending cap (repo/token.go's
// ValidateUserToken guards at :182 and :218), not the user's wallet balance — every test in this file seeds the owning
// user with a FULL wallet (user.Quota=1_000_000 in
// l3SetupTokenQuotaRouter), and a wallet top-up cannot raise a token's own
// remain_quota (token_service.go's actual remedy: edit the token or set it
// unlimited). The 402 instead carries {"reason":"token_quota_exhausted",
// "token_remain_quota_units":N} — see TestR2TokenAuth_QuotaExhausted_NoWalletTopupURL
// for the full contract check.
//
// Reverse lock: a disabled token must still get 401 — only the
// ErrTokenQuotaExhausted path is rerouted to 402, every other
// ValidateUserToken failure keeps its existing status code.
//
// Setup mirrors setupScopeTestRouter in auth_scope_test.go (same package,
// same harness shape) but is kept separate (l3_ prefix) because it needs to
// control UnlimitedQuota/RemainQuota/Status per-test, which the shared scope
// helper hardcodes to UnlimitedQuota:true.

import (
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

var l3TokenQuotaTestDBCounter atomic.Int64

// l3SetupTokenQuotaRouter builds an isolated sqlite-backed Token row with the
// given status/quota and wraps the real TokenAuth() middleware around a
// no-op 200 handler.
func l3SetupTokenQuotaRouter(t *testing.T, status int, unlimitedQuota bool, remainQuota int) (*gin.Engine, string, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:l3_quota_test_%d?mode=memory&cache=shared", l3TokenQuotaTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{&repo.User{}, &repo.Token{}}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
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

	user := &repo.User{
		Username:    "l3quotatestuser",
		DisplayName: "L3 Quota Test User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "l3quotatest@local",
		TenantId:    "default",
		Quota:       1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tokenKey := common.GetRandomString(48)
	token := &repo.Token{
		UserId:         user.Id,
		TenantId:       "default",
		Key:            tokenKey,
		Status:         status,
		Name:           "l3-quota-test",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: unlimitedQuota,
		RemainQuota:    remainQuota,
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) }
	r.POST("/v1/chat/completions", TokenAuth(), pass)

	cleanup := func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
	return r, tokenKey, cleanup
}

func l3DoProbe(t *testing.T, r *gin.Engine, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestL3TokenAuth_QuotaExhausted_Returns402NoLeak is the headline fix:
// remain_quota=0 must produce 402 (not a bare 401) carrying structured,
// non-leaking metadata. R2/B2 corrected the metadata contract itself — see
// TestR2TokenAuth_QuotaExhausted_NoWalletTopupURL for the reason/
// token_remain_quota_units/no-topup_url assertions; this test keeps the original
// key/internal-expression leak checks that were the other half of the D4 fix.
func TestL3TokenAuth_QuotaExhausted_Returns402NoLeak(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusEnabled, false, 0)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (quota-exhausted token); body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Message  string          `json:"message"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if len(body.Error.Metadata) == 0 {
		t.Fatalf("expected non-empty error.metadata (token-cap guidance), got none; body=%s", w.Body.String())
	}

	if strings.Contains(w.Body.String(), key[:3]) {
		t.Errorf("response body leaks key prefix: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "UnlimitedQuota") {
		t.Errorf("response body leaks internal Go expression: %s", w.Body.String())
	}
}

// TestL3TokenAuth_StatusExhausted_Returns402 covers the other
// ErrTokenQuotaExhausted source (Status==TokenStatusExhausted).
func TestL3TokenAuth_StatusExhausted_Returns402(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusExhausted, false, 0)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (exhausted-status token); body=%s", w.Code, w.Body.String())
	}
}

// TestL3TokenAuth_Disabled_Still401 is the reverse lock: only the quota
// sentinel reroutes to 402. Every other ValidateUserToken failure — here, a
// disabled token — must keep its pre-existing 401.
func TestL3TokenAuth_Disabled_Still401(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusDisabled, true, 1000)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (disabled token must NOT be rerouted to 402); body=%s", w.Code, w.Body.String())
	}
}

// TestR2TokenAuth_QuotaExhausted_NoWalletTopupURL is the B2 regression lock:
// the live probe that exposed the defect was a token with RemainQuota=0 while
// its OWNING USER'S WALLET WAS FULL (user.Quota=1_000_000, set by
// l3SetupTokenQuotaRouter for every case in this file) — proving the 402's
// topup_url was never actionable, because a wallet top-up cannot raise a
// token's own remain_quota. The 402 must instead carry the token-management
// hint (reason + token_remain_quota_units) and a request id, matching every other
// TokenAuth rejection.
func TestR2TokenAuth_QuotaExhausted_NoWalletTopupURL(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusEnabled, false, 0)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (quota-exhausted token); body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Message  string          `json:"message"`
			Code     string          `json:"code"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if body.Error.Code != "token_quota_exhausted" {
		t.Errorf("error.code = %q, want %q; body=%s", body.Error.Code, "token_quota_exhausted", w.Body.String())
	}
	if !strings.Contains(body.Error.Message, "(request id:") {
		t.Errorf("error.message = %q, want it to contain %q", body.Error.Message, "(request id:")
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(body.Error.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v; metadata=%s", err, body.Error.Metadata)
	}
	if _, present := meta["topup_url"]; present {
		t.Errorf("wallet is full (user.Quota=1_000_000) — response must NOT carry a wallet topup_url for a token-cap 402, got: %s", body.Error.Metadata)
	}
	if meta["reason"] != "token_quota_exhausted" {
		t.Errorf(`metadata["reason"] = %v, want "token_quota_exhausted"`, meta["reason"])
	}
	if _, present := meta["token_remain_quota_units"]; !present {
		t.Errorf("metadata missing token_remain_quota_units; metadata=%s", body.Error.Metadata)
	}
	if _, present := meta["token_remain_quota"]; present {
		t.Errorf("metadata must use the unambiguous token_remain_quota_units key, not the old token_remain_quota name, got: %s", body.Error.Metadata)
	}
}

// TestR2TokenAuth_StatusExhaustedButRemainQuotaPositive_NotClaimedExhausted
// is the B2 follow-up: a token whose Status flag is TokenStatusExhausted but
// whose RemainQuota has since been raised (5000, no Status flip — see
// repo/token.go's ValidateUserToken) must still 402 with token-management
// guidance, but the message must not falsely claim the quota is exhausted
// and the metadata reason must be distinguishable from the genuine
// quota-cap case so a client can render the correct remedy ("re-enable"
// rather than "top up/raise your quota").
func TestR2TokenAuth_StatusExhaustedButRemainQuotaPositive_NotClaimedExhausted(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusExhausted, false, 5000)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (still routes through the token-cap 402, just with a different message); body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Message  string          `json:"message"`
			Code     string          `json:"code"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if strings.Contains(body.Error.Message, "请修改令牌剩余额度") {
		t.Errorf("message must not tell the caller to edit remain_quota when RemainQuota=5000: %q", body.Error.Message)
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(body.Error.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v; metadata=%s", err, body.Error.Metadata)
	}
	if meta["reason"] == "token_quota_exhausted" {
		t.Errorf(`metadata["reason"] = %v, must NOT be "token_quota_exhausted" when remain_quota is positive`, meta["reason"])
	}
	remain, ok := meta["token_remain_quota_units"].(float64)
	if !ok || remain != 5000 {
		t.Errorf(`metadata["token_remain_quota_units"] = %v, want 5000`, meta["token_remain_quota_units"])
	}
	if _, present := meta["topup_url"]; present {
		t.Errorf("must NOT carry a wallet topup_url, got: %s", body.Error.Metadata)
	}
}

// TestR4BTokenAuth_StatusExhaustedUnlimitedQuotaRemainZero_TokenDisabledReason
// is the R3-B escaped-mutation lock: Status==TokenStatusExhausted +
// UnlimitedQuota=true + RemainQuota=0 is reachable (an admin can flip a
// token to unlimited without also re-enabling it — same non-atomic-update
// gap TestR2TokenAuth_StatusExhaustedButRemainQuotaPositive_NotClaimedExhausted
// covers for the RemainQuota>0 sibling). Before this test, deleting the
// `token.UnlimitedQuota ||` half of auth.go's quotaAvailable expression left
// this whole package green — the metadata's reason still fell through to
// "token_quota_exhausted", telling a caller with an unlimited-quota token
// its quota was exhausted (self-contradictory with token_remain_quota_units
// staying at the metadata's raw 0, which is what an unlimited token always
// reports). reason must be "token_disabled": the real remedy is re-enabling
// the token, not touching a quota that was never capped.
func TestR4BTokenAuth_StatusExhaustedUnlimitedQuotaRemainZero_TokenDisabledReason(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusExhausted, true, 0)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Message  string          `json:"message"`
			Code     string          `json:"code"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(body.Error.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v; metadata=%s", err, body.Error.Metadata)
	}
	if meta["reason"] != "token_disabled" {
		t.Errorf(`metadata["reason"] = %v, want "token_disabled" (UnlimitedQuota=true means the cap was never the problem)`, meta["reason"])
	}
	if strings.Contains(body.Error.Message, "请修改令牌剩余额度") {
		t.Errorf("message must not tell the caller to edit remain_quota on an unlimited-quota token: %q", body.Error.Message)
	}
}
