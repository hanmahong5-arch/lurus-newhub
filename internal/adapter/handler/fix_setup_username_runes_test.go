package handler

// fix_setup_username_runes_test.go — 回归测试：首启初始化的用户名长度校验。
// 原实现用 len(string)（UTF-8 字节数）判 1-12，提示语却写「1-12 个字符」，
// 于是 5 个汉字（15 字节）的合法用户名在首次建站时被拒。

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
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var fixSetupDBCounter atomic.Int64

// fixSetupEnv 准备一个空库（没有 root 用户，PostSetup 才会走用户名校验分支）
// 并把 setup 标志复位，结束时恢复全部全局态。
func fixSetupEnv(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:fixsetup%d?mode=memory&cache=shared", fixSetupDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Option{}, &repo.Setup{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			// SQLite 的索引名是全局的，多个模型共用同名复合索引时会报
			// already exists，可安全跳过（与包内既有测试脚手架一致）。
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevSQLite, prevPG, prevRedis := repo.DB, common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevSetup := constant.IsSetup()
	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
	constant.SetSetup(false)

	t.Cleanup(func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		constant.SetSetup(prevSetup)
	})
	return db
}

// fixSetupPost 用给定用户名调用 PostSetup，返回解析后的响应体。
func fixSetupPost(t *testing.T, username string) map[string]interface{} {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"username":           username,
		"SelfUseModeEnabled": false,
		"DemoSiteEnabled":    false,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")

	PostSetup(c)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return body
}

// TestFixPostSetup_CJKUsernameWithinRuneBound：5 个汉字（15 字节）在 1-12 个
// 字符的规则内，必须建站成功并落库。
func TestFixPostSetup_CJKUsernameWithinRuneBound(t *testing.T) {
	db := fixSetupEnv(t)

	const username = "测试管理员" // 5 runes / 15 bytes
	body := fixSetupPost(t, username)

	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("PostSetup success=false, message=%v（合法的 5 字用户名被按字节判超长）", body["message"])
	}
	var got repo.User
	if err := db.Where("username = ?", username).First(&got).Error; err != nil {
		t.Fatalf("root 用户未落库: %v", err)
	}
	if got.Role != common.RoleRootUser {
		t.Errorf("role = %d, want %d", got.Role, common.RoleRootUser)
	}
}

// TestFixPostSetup_RuneBoundStillEnforced：上界仍按字符计——13 个汉字必须被拒；
// 空用户名也仍被拒。保证修复没有把校验整个放开。
func TestFixPostSetup_RuneBoundStillEnforced(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
	}{
		{"too_long_13_runes", "一二三四五六七八九十甲乙丙"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixSetupEnv(t)
			body := fixSetupPost(t, tc.username)
			if ok, _ := body["success"].(bool); ok {
				t.Fatalf("username %q 应被拒绝, got success=true", tc.username)
			}
			msg, _ := body["message"].(string)
			if !strings.Contains(msg, "1-12") {
				t.Errorf("message = %q, want 长度校验提示", msg)
			}
		})
	}
}
