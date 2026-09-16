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
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ============================================================================
// V2 Platform Admin — User Management Tests
// ============================================================================

// TestListAdminUsersV2_Whitelist proves the list endpoint never leaks secret /
// internal columns (access_token, setting, remark, tenant_id, lurus_account_id),
// only the curated whitelist.
func TestListAdminUsersV2_Whitelist(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Plant a secret access token + remark on a user so a leak would be visible.
	secret := "supersecrettoken0000000000000000"
	ctx.NormalUser.SetAccessToken(secret)
	ctx.NormalUser.Remark = "internal note"
	if err := ctx.DB.Save(ctx.NormalUser).Error; err != nil {
		t.Fatalf("failed to seed user secrets: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodGet, "/api/v2/admin/users", nil, []string{"root"})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	users := data["users"].([]interface{})
	if len(users) == 0 {
		t.Fatal("expected at least one user in the admin list")
	}
	for _, raw := range users {
		u := raw.(map[string]interface{})
		for _, f := range []string{"access_token", "setting", "remark", "tenant_id", "lurus_account_id", "DeletedAt"} {
			if _, exists := u[f]; exists {
				t.Errorf("forbidden field %q leaked through adminUserView", f)
			}
		}
		// The curated fields must be present.
		if _, ok := u["username"]; !ok {
			t.Error("expected username field in adminUserView")
		}
	}
}

// TestListAdminUsersV2_StatusFilter exercises the status filter path.
func TestListAdminUsersV2_StatusFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Disable the normal user; the others stay enabled.
	ctx.NormalUser.Status = common.UserStatusDisabled
	if err := ctx.DB.Save(ctx.NormalUser).Error; err != nil {
		t.Fatalf("failed to disable user: %v", err)
	}

	path := fmt.Sprintf("/api/v2/admin/users?status=%d", common.UserStatusDisabled)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodGet, path, nil, []string{"root"})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	users := resp["data"].(map[string]interface{})["users"].([]interface{})
	if len(users) != 1 {
		t.Fatalf("expected exactly 1 disabled user, got %d", len(users))
	}
	got := users[0].(map[string]interface{})
	if int(got["id"].(float64)) != ctx.NormalUser.Id {
		t.Errorf("status filter returned the wrong user: %v", got["id"])
	}
}

// TestUpdateAdminUserV2_RoleStatusQuota updates the three mutable fields and
// asserts persistence via the returned view.
func TestUpdateAdminUserV2_RoleStatusQuota(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"role":   common.RoleAdminUser,
		"status": common.UserStatusDisabled,
		"quota":  4242,
		"group":  "vip",
	}
	path := fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut, path, body, []string{"root"})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	d := resp["data"].(map[string]interface{})
	if int(d["role"].(float64)) != common.RoleAdminUser {
		t.Errorf("expected role=%d, got %v", common.RoleAdminUser, d["role"])
	}
	if int(d["status"].(float64)) != common.UserStatusDisabled {
		t.Errorf("expected status=%d, got %v", common.UserStatusDisabled, d["status"])
	}
	if int(d["quota"].(float64)) != 4242 {
		t.Errorf("expected quota=4242, got %v", d["quota"])
	}
	if d["group"] != "vip" {
		t.Errorf("expected group=vip, got %v", d["group"])
	}
}

// TestUpdateAdminUserV2_DemotionRevokesPermissionGrants is the B-F5 oracle
// for the UpdateAdminUserV2 call site (cycle-8 L4 repair round): demoting a
// grant-holding admin below common.RoleAdminUser must revoke their
// delegated permission grant, not leave it active for a later
// re-promotion to silently restore.
func TestUpdateAdminUserV2_DemotionRevokesPermissionGrants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if _, _, err := repo.CreatePermissionGrant(ctx.AdminUser.Id, "audit", "read", ctx.RootUser.Id); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	granted, err := repo.HasActivePermissionGrant(ctx.AdminUser.Id, "audit", "read")
	if err != nil || !granted {
		t.Fatalf("HasActivePermissionGrant before demotion = (%v,%v), want (true,nil)", granted, err)
	}

	body := map[string]interface{}{"role": common.RoleCommonUser}
	path := fmt.Sprintf("/api/v2/admin/users/%d", ctx.AdminUser.Id)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut, path, body, []string{"root"})
	AssertV2Status(t, w, http.StatusOK)

	granted, err = repo.HasActivePermissionGrant(ctx.AdminUser.Id, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after demotion: %v", err)
	}
	if granted {
		t.Fatalf("permission grant still active after demoting the grantee below RoleAdminUser")
	}
}

// TestUpdateAdminUserV2_SelfDemotion proves the v2 role-update path applies
// app.CheckSelfDemotion: an actor (even root) cannot lower their own role into
// an admin lockout, while role-omitted self-updates and root editing another
// user's role keep working.
func TestUpdateAdminUserV2_SelfDemotion(t *testing.T) {
	cases := []struct {
		name       string
		targetSelf bool // target = caller (RootUser) vs another user (NormalUser)
		body       map[string]interface{}
		wantStatus int
		wantRole   int // expected role persisted in DB after the request
	}{
		{
			name:       "root self-demotion rejected, DB unchanged",
			targetSelf: true,
			body:       map[string]interface{}{"role": common.RoleAdminUser},
			wantStatus: http.StatusForbidden,
			wantRole:   common.RoleRootUser,
		},
		{
			name:       "role-omitted self-update still allowed",
			targetSelf: true,
			body:       map[string]interface{}{"quota": 777},
			wantStatus: http.StatusOK,
			wantRole:   common.RoleRootUser,
		},
		{
			name:       "root changing another user's role still works",
			targetSelf: false,
			body:       map[string]interface{}{"role": common.RoleAdminUser},
			wantStatus: http.StatusOK,
			wantRole:   common.RoleAdminUser,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := SetupV2TestRouter(t)
			defer ctx.Cleanup()

			target := ctx.NormalUser
			if tc.targetSelf {
				target = ctx.RootUser
			}

			path := fmt.Sprintf("/api/v2/admin/users/%d", target.Id)
			w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut, path, tc.body, []string{"root"})
			if tc.wantStatus == http.StatusOK {
				AssertV2Status(t, w, http.StatusOK)
				AssertV2Success(t, w)
			} else {
				AssertV2Error(t, w, tc.wantStatus)
			}

			var got repo.User
			if err := ctx.DB.First(&got, "id = ?", target.Id).Error; err != nil {
				t.Fatalf("failed to re-read target user: %v", err)
			}
			if got.Role != tc.wantRole {
				t.Errorf("expected persisted role=%d, got %d", tc.wantRole, got.Role)
			}
		})
	}
}

// TestDeleteAdminUserV2_SelfGuard refuses to let an admin delete their own
// account.
func TestDeleteAdminUserV2_SelfGuard(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	path := fmt.Sprintf("/api/v2/admin/users/%d", ctx.RootUser.Id)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodDelete, path, nil, []string{"root"})
	AssertV2Error(t, w, http.StatusBadRequest)
}

// TestDeleteAdminUserV2_Success deletes another (non-root) user.
func TestDeleteAdminUserV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	path := fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodDelete, path, nil, []string{"root"})
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)
}

// TestAdminUsersV2_NonRootRejected confirms a non-root caller is forbidden from
// every admin user endpoint.
func TestAdminUsersV2_NonRootRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/admin/users", nil, nil)
	AssertV2Error(t, w, http.StatusForbidden)

	path := fmt.Sprintf("/api/v2/admin/users/%d", ctx.AdminUser.Id)
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{"quota": 1}, nil)
	AssertV2Error(t, w, http.StatusForbidden)

	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, path, nil, nil)
	AssertV2Error(t, w, http.StatusForbidden)
}

// ---------------------------------------------------------------------------
// TestAdminRevokeUserSessions_AuditsReason (L7, auth-security-08/26/29) —
// self-contained setup (mirrors setupRoutingTestRouter/setupPricingWriteRouter
// in this package): SetupV2TestRouter's shared harness does not migrate
// entity.AuditEvent/UserSession or register this route, so this test wires
// its own tiny router+db+pinnedAuditWriter instead of extending the shared
// one (which 20+ other tests in this file depend on staying exactly as-is).
// ---------------------------------------------------------------------------

var adminSessionsRevokeTestDBCounter atomic.Int64

func setupAdminSessionsRevokeRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:adminsessionsrevoke%d?mode=memory&cache=shared", adminSessionsRevokeTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&entity.UserSession{}, &entity.AuditEvent{}, &entity.AuditChainHead{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})
	repo.DB = db

	r := gin.New()
	mockRoot := func(c *gin.Context) {
		c.Set("id", 999)
		c.Set("role", common.RoleRootUser)
		c.Next()
	}
	r.DELETE("/api/v2/admin/users/:id/sessions", mockRoot, RevokeUserSessionsAdminV2)

	t.Cleanup(func() {
		repo.DB = prevDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return r, db
}

// TestAdminRevokeUserSessions_AuditsReason: root revoking a compromised
// user's sessions revokes every active row with reason "admin_revoked" and
// leaves a durable audit.session_revoked row carrying that reason — distinct
// from a user's own self-service revoke-others.
func TestAdminRevokeUserSessions_AuditsReason(t *testing.T) {
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	r, db := setupAdminSessionsRevokeRouter(t)
	const targetUserID = 321

	now := time.Now().Unix()
	for i, key := range []string{"admin-target-1", "admin-target-2"} {
		if err := db.Create(&entity.UserSession{
			SessionKey: key, UserId: targetUserID, TenantId: "default",
			CreatedAt: now - int64(i), LastSeenAt: now - int64(i),
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/users/%d/sessions", targetUserID), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	data := resp["data"].(map[string]interface{})
	if data["revoked"].(float64) != 2 {
		t.Errorf("revoked = %v, want 2", data["revoked"])
	}

	var rows []entity.UserSession
	db.Where("user_id = ?", targetUserID).Find(&rows)
	for _, row := range rows {
		if row.RevokedAt == 0 || row.RevokeReason != entity.SessionRevokeReasonAdminRevoked {
			t.Errorf("session %s not revoked as expected: revoked_at=%d reason=%q", row.SessionKey, row.RevokedAt, row.RevokeReason)
		}
	}

	event := pollAuditRow(t, governance.ActionAuthSessionRevoked, 2*time.Second)
	if event == nil {
		t.Fatal("no auth.session_revoked audit row appeared")
	}
	if !strings.Contains(event.Details, entity.SessionRevokeReasonAdminRevoked) {
		t.Errorf("audit event details = %q, want it to contain %q", event.Details, entity.SessionRevokeReasonAdminRevoked)
	}
}

// TestAdminRevokeUserSessions_FlagOff: with SESSION_REGISTRY_ENABLED unset
// (default off), DELETE /api/v2/admin/users/:id/sessions answers
// {"revoked":0} WITHOUT touching the DB, even when rows exist (left over
// from a prior flag-on soak) — a rollback must not let this endpoint revoke
// anything.
func TestAdminRevokeUserSessions_FlagOff(t *testing.T) {
	t.Setenv("SESSION_REGISTRY_ENABLED", "false")
	r, db := setupAdminSessionsRevokeRouter(t)
	const targetUserID = 322

	now := time.Now().Unix()
	if err := db.Create(&entity.UserSession{
		SessionKey: "admin-target-flagoff", UserId: targetUserID, TenantId: "default",
		CreatedAt: now, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/users/%d/sessions", targetUserID), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	data := resp["data"].(map[string]interface{})
	if data["revoked"].(float64) != 0 {
		t.Errorf("revoked = %v, want 0 — the flag-off endpoint must not touch the DB", data["revoked"])
	}

	var row entity.UserSession
	if err := db.Where("session_key = ?", "admin-target-flagoff").First(&row).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.RevokedAt != 0 {
		t.Errorf("row revoked_at = %d, want 0", row.RevokedAt)
	}
}

// TestUpdateUser_DemotionRevokesPermissionGrants covers the OTHER live admin
// role-update path. PUT /api/user/ (router/api-router.go, AdminAuth) reaches
// handler.UpdateUser, which is a separate code path from UpdateAdminUserV2 —
// a grant that survives there is the same silent re-promotion hole B-F5
// describes, just through the v1 console API.
func TestUpdateUser_DemotionRevokesPermissionGrants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if _, _, err := repo.CreatePermissionGrant(ctx.AdminUser.Id, "audit", "read", ctx.RootUser.Id); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	// What AdminAuth puts on the context for a root caller.
	body, err := json.Marshal(map[string]interface{}{
		"id":           ctx.AdminUser.Id,
		"username":     ctx.AdminUser.Username,
		"display_name": ctx.AdminUser.DisplayName,
		"role":         common.RoleCommonUser,
		"quota":        ctx.AdminUser.Quota,
		"group":        ctx.AdminUser.Group,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/user/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", ctx.RootUser.Id)
	c.Set("role", common.RoleRootUser)

	UpdateUser(c)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateUser status = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("UpdateUser body = %s, want success", w.Body.String())
	}

	granted, err := repo.HasActivePermissionGrant(ctx.AdminUser.Id, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after demotion: %v", err)
	}
	if granted {
		t.Fatalf("permission grant still active after demoting the grantee through PUT /api/user/")
	}
}
