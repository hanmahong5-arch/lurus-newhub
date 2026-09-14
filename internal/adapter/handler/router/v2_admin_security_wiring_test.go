package router

// v2_admin_security_wiring_test.go — L6 (2026-09-12): proves the REAL
// production route table (SetApiV2Router), not a hand-mounted mimic, gates
// POST /api/v2/admin/security/users/:id/totp/force-disable behind the
// acting root's OWN step-up (SecureVerificationRequired), the same way
// root_seal_test.go proves the v1 admin-vs-root seal against the real
// SetApiRouter. handler package unit tests
// (TestAdminTotpForceDisable_RequiresStepUp) mount the middleware chain by
// hand mirroring this wiring — this test is what catches a future edit to
// api-v2-router.go that drops the middleware from the real chain, which the
// handler-level test structurally cannot see.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

var adminSecWiringDBCounter atomic.Int64

// TestSetApiV2Router_ForceDisableTotp_RequiresOwnStepUp seeds a real root
// session (matching root_seal_test.go's convention: identity injected into
// the gin session, then served through the REAL SetApiV2Router) and proves
// a root who has NOT stepped up in this session is refused with
// VERIFICATION_REQUIRED — never silently allowed through on the session's
// root role alone. Mutation target: removing
// middleware.SecureVerificationRequired() from the force-disable route in
// api-v2-router.go turns this red.
func TestSetApiV2Router_ForceDisableTotp_RequiresOwnStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:adminsecwiring%d?mode=memory&cache=shared", adminSecWiringDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Log{}, &entity.Tenant{}, &entity.UserTOTP{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	rootRow := &repo.User{
		Username: "wiring_root", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Email: "wiring_root@local", TenantId: "default",
	}
	if err := db.Create(rootRow).Error; err != nil {
		t.Fatalf("create root: %v", err)
	}
	targetRow := &repo.User{
		Username: "wiring_target", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "wiring_target@local", TenantId: "default",
	}
	if err := db.Create(targetRow).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := db.Create(&entity.UserTOTP{
		UserId: targetRow.Id, SecretEncrypted: "enc-fixture", Enabled: true,
		CreatedAt: common.GetTimestamp(), ConfirmedAt: common.GetTimestamp(),
	}).Error; err != nil {
		t.Fatalf("seed target totp: %v", err)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("admin-sec-wiring-test-secret"))
	engine.Use(sessions.Sessions("session", store))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", rootRow.Username)
		s.Set("role", rootRow.Role)
		s.Set("id", rootRow.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiV2Router(engine)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/v2/admin/security/users/%d/totp/force-disable", targetRow.Id),
		strings.NewReader(`{"reason":"support ticket"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("force-disable via real router, root session with no step-up: status=%d body=%s (want 403)",
			w.Code, w.Body.String())
	}
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Code != "VERIFICATION_REQUIRED" {
		t.Fatalf("code = %q, want VERIFICATION_REQUIRED — body=%s", env.Code, w.Body.String())
	}

	// The enrollment must survive: the gate ran before the handler.
	var stillEnrolled entity.UserTOTP
	if err := db.Where("user_id = ?", targetRow.Id).First(&stillEnrolled).Error; err != nil {
		t.Fatalf("target enrollment must survive a rejected force-disable: %v", err)
	}
}
