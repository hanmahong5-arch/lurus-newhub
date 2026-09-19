package router

// task_list_filters_mount_test.go — cycle-8 L10 repair, ruling A-F2
// (REAL-CHAIN RULE): the enterprise claim "a user cannot enumerate another
// project's tasks via the filter" was previously locked only by
// handler.TestGetUserTask_ForeignProjectFilterIsEmpty, which hand-seeds
// c.Set("id", ...) and calls handler.GetUserTask directly — it never drives
// the REAL route table (SetApiRouter) or middleware.UserAuth/AdminAuth. This
// test does: a cookie session authenticated the same way production
// authenticates (gin-contrib/sessions), through the real GET /api/task/self/
// and GET /api/task/ routes.
//
// Mirrors mountRealRouterWithSession's fixture shape (audit_routes_root_or_
// granted_test.go) but for SetApiRouter (task routes live there, not
// SetApiV2Router) and with a DB-backed repo.User row so middleware.UserAuth/
// AdminAuth's session re-validation (resolveSessionIdentity -> GetUserCache
// -> GetUserById) resolves a real user instead of failing the "invalid
// session data" branch.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var taskListFiltersMountDBCounter atomic.Int64

// taskListFiltersMountFixture is the DB-backed fixture shared by every
// subtest below: two users (a common user who owns project 5, and an
// admin), and two tasks (one under project 5 owned by the common user, one
// under project 999 owned by neither). serveAs builds a fresh real-router
// engine per call so each request carries its own session identity.
type taskListFiltersMountFixture struct {
	commonUser *repo.User
	adminUser  *repo.User
	rootUser   *repo.User
}

func mountTaskListFiltersFixture(t *testing.T) *taskListFiltersMountFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:tasklistfiltersmount%d?mode=memory&cache=shared", taskListFiltersMountDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Task{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.RedisEnabled = false
	t.Cleanup(func() {
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	commonUser := &repo.User{
		Username: "task-filter-common-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "task-filter-common@local", TenantId: "default",
	}
	if err := db.Create(commonUser).Error; err != nil {
		t.Fatalf("create common user: %v", err)
	}
	adminUser := &repo.User{
		Username: "task-filter-admin-user", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Email: "task-filter-admin@local", TenantId: "default",
	}
	if err := db.Create(adminUser).Error; err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	ownTask := &repo.Task{TaskID: "own-project-5", Platform: constant.TaskPlatformSuno, UserId: commonUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 5, RequestId: "req-own-5"}
	if err := db.Create(ownTask).Error; err != nil {
		t.Fatalf("seed own task: %v", err)
	}
	foreignTask := &repo.Task{TaskID: "foreign-project-999", Platform: constant.TaskPlatformSuno, UserId: adminUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 999, RequestId: "req-foreign-999"}
	if err := db.Create(foreignTask).Error; err != nil {
		t.Fatalf("seed foreign task: %v", err)
	}

	rootUser := &repo.User{
		Username: "task-filter-root-user", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Email: "task-filter-root@local", TenantId: "default",
	}
	if err := db.Create(rootUser).Error; err != nil {
		t.Fatalf("create root user: %v", err)
	}

	// cycle-11 L8/W: a user in a DIFFERENT tenant with its own task row —
	// commonUser/adminUser above are both tenant "default", so this is the
	// only row that proves the admin route's TenantScoped filter (task.go's
	// GetAllTask) actually narrows by tenant rather than merely returning
	// "everything", and that root (no TenantScoped filter) still sees it.
	otherTenantUser := &repo.User{
		Username: "task-filter-other-tenant-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "task-filter-other@local", TenantId: "other-tenant-xyz",
	}
	if err := db.Create(otherTenantUser).Error; err != nil {
		t.Fatalf("create other-tenant user: %v", err)
	}
	otherTenantTask := &repo.Task{TaskID: "other-tenant-task", Platform: constant.TaskPlatformSuno, UserId: otherTenantUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 1, RequestId: "req-other-tenant"}
	if err := db.Create(otherTenantTask).Error; err != nil {
		t.Fatalf("seed other-tenant task: %v", err)
	}

	return &taskListFiltersMountFixture{commonUser: commonUser, adminUser: adminUser, rootUser: rootUser}
}

// serveAs issues a request against the fixture's real router with a cookie
// session identifying the given user — the same session shape
// resolveSessionIdentity (middleware/auth.go) reads in production.
func (f *taskListFiltersMountFixture) serveAs(t *testing.T, user *repo.User, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	sessionEngine := gin.New()
	sessionEngine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("task-list-filters-mount-test-secret"))
	sessionEngine.Use(sessions.Sessions("session", store))
	sessionEngine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", user.Username)
		s.Set("role", user.Role)
		s.Set("id", user.Id)
		s.Set("status", user.Status)
		_ = s.Save()
		c.Next()
	})
	SetApiRouter(sessionEngine)

	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	sessionEngine.ServeHTTP(w, req)
	return w
}

type taskListResp struct {
	Success bool `json:"success"`
	Data    struct {
		Total int `json:"total"`
	} `json:"data"`
}

func decodeTaskListResp(t *testing.T, w *httptest.ResponseRecorder) taskListResp {
	t.Helper()
	var resp taskListResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	return resp
}

// TestTaskListFilters_SelfRoute_RealChain drives GET /api/task/self/ through
// the REAL SetApiRouter + middleware.UserAuth chain with a cookie session —
// not a hand-seeded gin.Context — and proves the ownership narrowing the
// operator's enterprise_acceptance criterion names ("a user cannot
// enumerate another project's tasks via the filter") holds on that real
// path.
func TestTaskListFilters_SelfRoute_RealChain(t *testing.T) {
	f := mountTaskListFiltersFixture(t)

	w := f.serveAs(t, f.commonUser, http.MethodGet, "/api/task/self/?project_id=5")
	resp := decodeTaskListResp(t, w)
	if !resp.Success || resp.Data.Total != 1 {
		t.Fatalf("own project_id: status=%d success=%v total=%d, want 200 success total=1; body=%s", w.Code, resp.Success, resp.Data.Total, w.Body.String())
	}

	w = f.serveAs(t, f.commonUser, http.MethodGet, "/api/task/self/?project_id=999")
	resp = decodeTaskListResp(t, w)
	if w.Code != http.StatusOK || !resp.Success || resp.Data.Total != 0 {
		t.Fatalf("foreign project_id: status=%d success=%v total=%d, want 200 success total=0 (fail-closed empty page, not an error); body=%s", w.Code, resp.Success, resp.Data.Total, w.Body.String())
	}
}

// TestTaskListFilters_AdminRoute_RealChain drives GET /api/task/ through the
// REAL SetApiRouter + middleware.AdminAuth chain: an admin session's
// request_id filter reaches the row regardless of who owns it (no
// ownership narrowing on the admin list, per spec), and a common-user
// session on the same admin-only route is rejected before it ever reaches
// the handler.
func TestTaskListFilters_AdminRoute_RealChain(t *testing.T) {
	f := mountTaskListFiltersFixture(t)

	w := f.serveAs(t, f.adminUser, http.MethodGet, "/api/task/?request_id=req-foreign-999")
	resp := decodeTaskListResp(t, w)
	if !resp.Success || resp.Data.Total != 1 {
		t.Fatalf("admin request_id filter: status=%d success=%v total=%d, want 200 success total=1; body=%s", w.Code, resp.Success, resp.Data.Total, w.Body.String())
	}

	w = f.serveAs(t, f.commonUser, http.MethodGet, "/api/task/")
	resp = decodeTaskListResp(t, w)
	if resp.Success {
		t.Fatalf("common user on admin route: success=%v, want false (AdminAuth must reject); body=%s", resp.Success, w.Body.String())
	}
}

// TestTaskListFilters_AdminRoute_TenantScoped_RealChain is the cycle-11
// L8/W REAL-CHAIN oracle for GetAllTask's TenantScoped filter (task.go):
// a non-root admin session must see only its own tenant's rows (ownTask +
// foreignTask, both tenant "default" — otherTenantTask, owned by a user in
// "other-tenant-xyz", must not leak in), while a root session (no
// TenantScoped filter applied) sees every tenant's rows including it.
//
// Mutation: deleting the `queryParams.TenantScoped = true` /
// `queryParams.TenantID = ...` assignment in internal/adapter/handler/task.go
// (GetAllTask) turns the admin sub-test red (total becomes 3, the
// other-tenant row leaks in).
func TestTaskListFilters_AdminRoute_TenantScoped_RealChain(t *testing.T) {
	f := mountTaskListFiltersFixture(t)

	w := f.serveAs(t, f.adminUser, http.MethodGet, "/api/task/")
	resp := decodeTaskListResp(t, w)
	if !resp.Success || resp.Data.Total != 2 {
		t.Fatalf("tenant admin total=%d success=%v, want 2 (ownTask+foreignTask, both tenant default; other-tenant-task must not leak); body=%s", resp.Data.Total, resp.Success, w.Body.String())
	}

	wRoot := f.serveAs(t, f.rootUser, http.MethodGet, "/api/task/")
	respRoot := decodeTaskListResp(t, wRoot)
	if !respRoot.Success || respRoot.Data.Total != 3 {
		t.Fatalf("root total=%d success=%v, want 3 (every tenant's rows, no TenantScoped filter); body=%s", respRoot.Data.Total, respRoot.Success, wRoot.Body.String())
	}
}
