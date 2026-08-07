package handler

// cov_handler-deploy_setup_test.go — business-acceptance tests for setup.go:
// first-run bootstrap (GetSetup / PostSetup). Money-path relevant framing:
// setup grants the FIRST root account with Quota=100000000, so double-setup
// (replay) and the TOCTOU race between concurrent bootstrap requests are the
// two edges that matter most.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var handlerDeploySetupDBCounter atomic.Int64

// handlerDeploySetupFixture bundles an isolated SQLite DB + router for
// setup.go tests and restores every package-level global it touches
// (repo.DB/LOG_DB, common.UsingSQLite/RedisEnabled, constant's setup flag,
// operation_setting's two mode flags) on cleanup.
type handlerDeploySetupFixture struct {
	router *gin.Engine
	db     *gorm.DB
}

func handlerDeploySetupNewFixture(t *testing.T) *handlerDeploySetupFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:covsetup%d?mode=memory&cache=shared", handlerDeploySetupDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Setup{}, &repo.Option{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("automigrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevRedis := common.UsingSQLite, common.RedisEnabled
	prevSetup := constant.IsSetup()
	prevSelfUse, prevDemo := operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.RedisEnabled = false
	constant.SetSetup(false)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.RedisEnabled = prevSQLite, prevRedis
		constant.SetSetup(prevSetup)
		operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled = prevSelfUse, prevDemo
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	r := gin.New()
	r.GET("/setup", GetSetup)
	r.POST("/setup", PostSetup)
	return &handlerDeploySetupFixture{router: r, db: db}
}

func handlerDeploySetupDo(f *handlerDeploySetupFixture, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func handlerDeploySetupParse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v raw=%s", err, w.Body.String())
	}
	return out
}

// ─── GetSetup ───────────────────────────────────────────────────────────────

func TestGetSetup_AlreadyInitialized_ShortCircuitsBeforeRootLookup(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(true)

	w := handlerDeploySetupDo(f, http.MethodGet, "/setup", nil)
	resp := handlerDeploySetupParse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %v", resp)
	}
	if data["status"] != true {
		t.Errorf("status = %v, want true", data["status"])
	}
	// Early-return branch never touches root_init/database_type — they stay
	// at Go zero values (false / "") rather than reflecting real DB state.
	if data["root_init"] != false {
		t.Errorf("root_init = %v, want false (early-return branch leaves it unset)", data["root_init"])
	}
	if data["database_type"] != "" {
		t.Errorf("database_type = %v, want empty string (early-return branch leaves it unset)", data["database_type"])
	}
}

func TestGetSetup_NotInitialized_NoRoot_ReportsPostgresByDefault(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)
	common.UsingSQLite = false // simulate the PG-only runtime

	w := handlerDeploySetupDo(f, http.MethodGet, "/setup", nil)
	resp := handlerDeploySetupParse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data["root_init"] != false {
		t.Errorf("root_init = %v, want false (no user rows seeded)", data["root_init"])
	}
	if data["database_type"] != "postgres" {
		t.Errorf("database_type = %v, want postgres", data["database_type"])
	}
}

func TestGetSetup_NotInitialized_RootExists_ReportsSQLiteTier(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)
	common.UsingSQLite = true
	if err := f.db.Create(&repo.User{Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatalf("seed root: %v", err)
	}

	w := handlerDeploySetupDo(f, http.MethodGet, "/setup", nil)
	resp := handlerDeploySetupParse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data["root_init"] != true {
		t.Errorf("root_init = %v, want true", data["root_init"])
	}
	if data["database_type"] != "sqlite" {
		t.Errorf("database_type = %v, want sqlite", data["database_type"])
	}
}

// ─── PostSetup: duplicate-setup rejection (idempotency / replay) ──────────

func TestPostSetup_AlreadyClaimed_RejectsWithoutTouchingDB(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(true) // simulate a prior successful boot claim

	w := handlerDeploySetupDo(f, http.MethodPost, "/setup", []byte(`{"username":"root2"}`))
	resp := handlerDeploySetupParse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false, got %v", resp["success"])
	}
	if resp["message"] != "系统已经初始化完成" {
		t.Errorf("message = %v, want 系统已经初始化完成", resp["message"])
	}
	var userCount int64
	f.db.Model(&repo.User{}).Count(&userCount)
	if userCount != 0 {
		t.Errorf("replay must not create a user row: count=%d", userCount)
	}
}

// TestPostSetup_ConcurrentClaim_ExactlyOneWinner is the core TOCTOU-race
// forcing function: N simultaneous first-boot requests must yield exactly
// one root account, never zero and never more than one.
func TestPostSetup_ConcurrentClaim_ExactlyOneWinner(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)

	const n = 10
	var wg sync.WaitGroup
	var successes atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := []byte(fmt.Sprintf(`{"username":"root%d"}`, idx))
			req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, req)
			var out map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			if out["success"] == true {
				successes.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("winners = %d, want exactly 1 (TryClaimSetup race)", got)
	}
	var userCount int64
	f.db.Model(&repo.User{}).Count(&userCount)
	if userCount != 1 {
		t.Errorf("user rows = %d, want exactly 1 (no duplicate root accounts under contention)", userCount)
	}
}

// ─── PostSetup: malformed input must revert the claim (defer path) ────────

func TestPostSetup_MalformedJSON_RevertsClaimForRetry(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)

	w := handlerDeploySetupDo(f, http.MethodPost, "/setup", []byte(`{not-json`))
	resp := handlerDeploySetupParse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false, got %v", resp["success"])
	}
	if resp["message"] != "请求参数有误" {
		t.Errorf("message = %v, want 请求参数有误", resp["message"])
	}
	if constant.IsSetup() {
		t.Errorf("claim was not reverted after a bind failure — a legitimate retry would now be permanently locked out")
	}
}

// TestPostSetup_UsernameLength_ByteLengthNotRuneLength documents the ACTUAL
// current behaviour: the 1-12 bound is applied to len(req.Username) — Go's
// byte length, not a rune/character count. A 5-character Chinese username is
// 15 UTF-8 bytes and gets rejected even though "5 characters" reads as well
// within a "1-12 characters" limit to a human.
//
// FINDING: this is very likely a business-logic bug for CJK usernames (the
// Chinese error message "长度必须在1-12个字符之间" literally promises a
// character count, not a byte count), but per instructions we do not modify
// the handler — this test locks in the CURRENT behaviour as a regression
// baseline pending an owner decision.
func TestPostSetup_UsernameLength_ByteLengthNotRuneLength(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)

	fiveCJKChars := "你好世界啊" // 5 runes, 15 UTF-8 bytes
	if len([]rune(fiveCJKChars)) != 5 {
		t.Fatalf("test fixture invariant broken: want 5 runes, got %d", len([]rune(fiveCJKChars)))
	}
	if len(fiveCJKChars) != 15 {
		t.Fatalf("test fixture invariant broken: want 15 bytes, got %d", len(fiveCJKChars))
	}

	w := handlerDeploySetupDo(f, http.MethodPost, "/setup",
		[]byte(fmt.Sprintf(`{"username":%q}`, fiveCJKChars)))
	resp := handlerDeploySetupParse(t, w)

	if resp["success"] != false {
		t.Fatalf("FINDING regression: a 5-character CJK username now passes validation (got success=%v) — "+
			"if this is intentional, this test should be updated; if not, deployment.go/setup.go still uses byte length", resp["success"])
	}
	if resp["message"] != "用户名长度必须在1-12个字符之间" {
		t.Errorf("message = %v, want the length-bound rejection message", resp["message"])
	}
	if constant.IsSetup() {
		t.Errorf("claim was not reverted after username-length rejection")
	}
}

func TestPostSetup_EmptyUsername_Rejected(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)

	w := handlerDeploySetupDo(f, http.MethodPost, "/setup", []byte(`{"username":""}`))
	resp := handlerDeploySetupParse(t, w)
	if resp["success"] != false || resp["message"] != "用户名长度必须在1-12个字符之间" {
		t.Errorf("got success=%v message=%v, want rejection", resp["success"], resp["message"])
	}
}

// ─── PostSetup: happy path — verifies actual persisted state, not just the
//     HTTP envelope. ───────────────────────────────────────────────────────

func TestPostSetup_Success_PersistsRootUserAndSetupRecordAndModes(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)

	w := handlerDeploySetupDo(f, http.MethodPost, "/setup",
		[]byte(`{"username":"boss","SelfUseModeEnabled":true,"DemoSiteEnabled":true}`))
	resp := handlerDeploySetupParse(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v body=%s", resp["success"], w.Body.String())
	}
	if resp["message"] != "系统初始化成功" {
		t.Errorf("message = %v, want 系统初始化成功", resp["message"])
	}

	var root repo.User
	if err := f.db.Where("username = ?", "boss").First(&root).Error; err != nil {
		t.Fatalf("root user not persisted: %v", err)
	}
	if root.Role != common.RoleRootUser {
		t.Errorf("role = %d, want RoleRootUser(%d)", root.Role, common.RoleRootUser)
	}
	if root.Quota != 100000000 {
		t.Errorf("quota = %d, want 100000000", root.Quota)
	}
	if root.TenantId != "default" {
		t.Errorf("tenant_id = %q, want default", root.TenantId)
	}

	var setupRow repo.Setup
	if err := f.db.First(&setupRow).Error; err != nil {
		t.Fatalf("setup record not persisted: %v", err)
	}

	if !operation_setting.SelfUseModeEnabled || !operation_setting.DemoSiteEnabled {
		t.Errorf("operation modes not applied in-process: self_use=%v demo=%v",
			operation_setting.SelfUseModeEnabled, operation_setting.DemoSiteEnabled)
	}

	var selfUseOpt repo.Option
	if err := f.db.Where("key = ?", "SelfUseModeEnabled").First(&selfUseOpt).Error; err != nil {
		t.Fatalf("SelfUseModeEnabled option not persisted: %v", err)
	}
	if selfUseOpt.Value != "true" {
		t.Errorf("SelfUseModeEnabled option value = %q, want true", selfUseOpt.Value)
	}

	if !constant.IsSetup() {
		t.Errorf("setup flag must remain claimed (committed=true) after success")
	}
}

func TestPostSetup_RootAlreadyExists_SkipsUsernameValidationAndCreatesNoDuplicate(t *testing.T) {
	f := handlerDeploySetupNewFixture(t)
	constant.SetSetup(false)
	if err := f.db.Create(&repo.User{Username: "pre-existing-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A username that would fail the length rule is irrelevant once a root
	// already exists — PostSetup must not even evaluate it.
	tooLong := "this-username-is-way-too-long-for-the-normal-rule"
	w := handlerDeploySetupDo(f, http.MethodPost, "/setup",
		[]byte(fmt.Sprintf(`{"username":%q}`, tooLong)))
	resp := handlerDeploySetupParse(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true (root-exists path skips username validation), got %v body=%s", resp["success"], w.Body.String())
	}

	var userCount int64
	f.db.Model(&repo.User{}).Count(&userCount)
	if userCount != 1 {
		t.Errorf("user rows = %d, want 1 (no second root created when one already exists)", userCount)
	}
}
