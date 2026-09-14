package handler

// v2_admin_system_tasks_test.go — L3 oracle coverage for GET
// /api/v2/admin/system/tasks. GetSystemTasksV2 reads two real, process-global
// production data sources (taskreg.Snapshot + metrics.LeaderTaskLastSuccess),
// so these tests drive both through their real exported API — taskreg.Register
// and the real GaugeVec — rather than hand-building a response struct. The
// root-only tests mount the PRODUCTION middleware.RootJWTAuth (same wiring as
// api-v2-router.go's adminRoute), matching the convention in
// v2_admin_analytics_test.go / tenant_invite_admin_test.go.
//
// taskreg is a process-wide singleton with no reset hook, so every test here
// registers its own uniquely-named ("systest-...") task rather than asserting
// on the full task list — other tests in this package (e.g.
// TestChannelHealthTest_SuccessfulTickStampsHeartbeat) register real
// production task names into the same registry, and assuming an exact/short
// list here would make this file order-dependent.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var systemTasksTestDBCounter atomic.Int64

// setupSystemTasksRootRouter mounts the real RootJWTAuth ahead of
// GetSystemTasksV2, exactly as api-v2-router.go's adminRoute does, plus a
// fake "already logged in" session seeder middleware so tests don't have to
// drive an actual OIDC/password login flow — the same convention
// v2_admin_security_wiring_test.go (router package) and
// TestSetApiV2Router_ForceDisableTotp_RequiresOwnStepUp use: it writes the
// session cookie fields RootJWTAuth's own authHelper reads (username, role,
// id, status), so RootJWTAuth's role check downstream is exercised for real.
func setupSystemTasksRootRouter(t *testing.T, role int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// authHelper (behind RootJWTAuth) tries repo.GetUserCache, which hits
	// Redis when common.RedisEnabled — leaked true from another test in this
	// package would otherwise panic on a nil common.RDB here. Force it off
	// and restore, matching the convention in billing_degrade_test.go etc.
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	// GetUserCache's Redis miss falls through to repo.GetUserById, which
	// panics on a nil repo.DB rather than erroring — give it a real (empty)
	// hermetic DB so that path returns an ordinary "record not found" and
	// authHelper's documented fail-open (keep the session's own role/status)
	// kicks in, exactly as it does in production when the cache/DB layer
	// briefly disagrees with an otherwise-valid session.
	dsn := fmt.Sprintf("file:systasksauth%d?mode=memory&cache=shared", systemTasksTestDBCounter.Add(1))
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&repo.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB := repo.DB
	repo.DB = gdb
	t.Cleanup(func() {
		repo.DB = prevDB
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	router := gin.New()
	store := cookie.NewStore([]byte("system-tasks-test-secret"))
	router.Use(sessions.Sessions("session", store))
	router.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", "systasks_actor")
		s.Set("role", role)
		s.Set("id", 1)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	admin := router.Group("/api/v2/admin")
	admin.Use(middleware.RootJWTAuth())
	admin.GET("/system/tasks", GetSystemTasksV2)
	return router
}

func doSystemTasksGET(router *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/system/tasks", nil)
	router.ServeHTTP(w, req)
	return w
}

// findTaskView locates one row by name in the decoded response body.
func findTaskView(t *testing.T, w *httptest.ResponseRecorder, name string) map[string]interface{} {
	t.Helper()
	body := parseJSON(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data object: %s", w.Body.String())
	}
	tasksRaw, ok := data["tasks"].([]interface{})
	if !ok {
		t.Fatalf("missing tasks array: %s", w.Body.String())
	}
	for _, raw := range tasksRaw {
		row, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if row["name"] == name {
			return row
		}
	}
	t.Fatalf("task %q not found in response: %s", name, w.Body.String())
	return nil
}

// TestSystemTasks_RootOnly: anonymous is 401'd by RootJWTAuth before the
// handler runs; a role-10 (admin, not root) session is turned away by
// authHelper's own minRole check (success:false, 200 — the legacy
// convention this codebase's auth layer uses, NOT a 403) without ever
// reaching GetSystemTasksV2; only a role-100 (root) session gets a
// success:true body with a tasks array.
func TestSystemTasks_RootOnly(t *testing.T) {
	t.Run("anonymous_401", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		store := cookie.NewStore([]byte("system-tasks-anon-test-secret"))
		router.Use(sessions.Sessions("session", store))
		admin := router.Group("/api/v2/admin")
		admin.Use(middleware.RootJWTAuth())
		admin.GET("/system/tasks", GetSystemTasksV2)

		w := doSystemTasksGET(router)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous: status = %d, want 401, body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("admin_role_refused", func(t *testing.T) {
		router := setupSystemTasksRootRouter(t, common.RoleAdminUser)
		w := doSystemTasksGET(router)
		body := parseJSON(t, w)
		if success, _ := body["success"].(bool); success {
			t.Errorf("admin (role=10) session: success = true, want false (root-only) — body=%s", w.Body.String())
		}
		if _, hasData := body["data"]; hasData {
			t.Errorf("admin (role=10) session must not reach the handler at all — body=%s", w.Body.String())
		}
	})

	t.Run("root_role_allowed", func(t *testing.T) {
		router := setupSystemTasksRootRouter(t, common.RoleRootUser)
		w := doSystemTasksGET(router)
		if w.Code != http.StatusOK {
			t.Fatalf("root session: status = %d, want 200, body=%s", w.Code, w.Body.String())
		}
		body := parseJSON(t, w)
		if success, _ := body["success"].(bool); !success {
			t.Errorf("root session: success = false, want true — body=%s", w.Body.String())
		}
		if _, hasData := body["data"].(map[string]interface{}); !hasData {
			t.Errorf("root session: missing data object — body=%s", w.Body.String())
		}
	})
}

// TestSystemTasks_JSONMatchesGauge proves the handler's per-task fields are
// read live from taskreg + the real gauge, not synthesised: register a task
// with a distinctive interval and leaderOnly value, stamp its gauge to a
// known unix timestamp, and check every field round-trips exactly.
func TestSystemTasks_JSONMatchesGauge(t *testing.T) {
	const name = "systest-json-match"
	taskreg.Register(name, func() time.Duration { return 90 * time.Second }, false, nil)
	stampedAt := common.GetTimestamp() - 5 // recent — well inside 2x90s, so "ok"
	metrics.LeaderTaskLastSuccess.WithLabelValues(name).Set(float64(stampedAt))

	router := setupSystemTasksRootRouter(t, common.RoleRootUser)
	w := doSystemTasksGET(router)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	row := findTaskView(t, w, name)
	if got := row["interval_seconds"]; got != 90.0 {
		t.Errorf("interval_seconds = %v, want 90", got)
	}
	if got, _ := row["leader_only"].(bool); got {
		t.Errorf("leader_only = true, want false")
	}
	if got := row["last_success_at"]; got != float64(stampedAt) {
		t.Errorf("last_success_at = %v, want %d", got, stampedAt)
	}
	if got := row["state"]; got != "ok" {
		t.Errorf("state = %v, want ok", got)
	}
	if got := row["standby_reason"]; got != "" {
		t.Errorf("standby_reason = %v, want empty string for a non-standby row", got)
	}
}

// TestSystemTasks_OverdueBoundary locks the ">2x interval" overdue threshold
// on both sides: a heartbeat exactly at the boundary must still read "ok"
// (strict greater-than, not >=), and just past it must read "overdue".
// Mutation target: an off-by-one or flipped comparison in GetSystemTasksV2's
// overdue arithmetic turns one of these two subtests red.
//
// L3 repair round (B-F9): uses the injected systemTasksNow clock, pinned to
// a fixed value, rather than comparing a gauge stamped from the real clock
// against a second, later real-clock read — the original shape could flip
// "exactly at the boundary" into "just past it" if a second elapsed between
// the two reads.
func TestSystemTasks_OverdueBoundary(t *testing.T) {
	router := setupSystemTasksRootRouter(t, common.RoleRootUser)

	const fixedNow int64 = 2_000_000_000
	prevClock := systemTasksNow
	systemTasksNow = func() int64 { return fixedNow }
	t.Cleanup(func() { systemTasksNow = prevClock })

	t.Run("exactly_at_2x_interval_is_ok", func(t *testing.T) {
		const name = "systest-overdue-boundary-ok"
		taskreg.Register(name, func() time.Duration { return 10 * time.Second }, false, nil)
		metrics.LeaderTaskLastSuccess.WithLabelValues(name).Set(float64(fixedNow - 20)) // == 2*10s

		w := doSystemTasksGET(router)
		row := findTaskView(t, w, name)
		if got := row["state"]; got != "ok" {
			t.Errorf("state at exactly 2x interval = %v, want ok (strict >, not >=)", got)
		}
	})

	t.Run("just_past_2x_interval_is_overdue", func(t *testing.T) {
		const name = "systest-overdue-boundary-over"
		taskreg.Register(name, func() time.Duration { return 10 * time.Second }, false, nil)
		metrics.LeaderTaskLastSuccess.WithLabelValues(name).Set(float64(fixedNow - 25)) // > 2*10s

		w := doSystemTasksGET(router)
		row := findTaskView(t, w, name)
		if got := row["state"]; got != "overdue" {
			t.Errorf("state at 25s since last success (interval=10s) = %v, want overdue", got)
		}
	})
}

// TestSystemTasks_NeverSucceeded_UptimeBoundary is the A-F2 oracle: a task
// that has never once stamped its gauge on this replica (lastSuccess == 0)
// and is active/non-leader-gated (so it falls through to the uptime
// branch) must read "overdue" once process uptime exceeds 2x its interval,
// and "ok" before that — pinned with the injected clock and a saved/
// restored common.StartTime, not the real clock.
func TestSystemTasks_NeverSucceeded_UptimeBoundary(t *testing.T) {
	router := setupSystemTasksRootRouter(t, common.RoleRootUser)

	const fixedNow int64 = 2_000_000_000
	prevClock := systemTasksNow
	systemTasksNow = func() int64 { return fixedNow }
	t.Cleanup(func() { systemTasksNow = prevClock })

	prevStart := common.StartTime
	common.StartTime = fixedNow - 100 // 100s of uptime
	t.Cleanup(func() { common.StartTime = prevStart })

	t.Run("short_interval_never_succeeded_is_overdue", func(t *testing.T) {
		const name = "systest-never-succeeded-overdue"
		taskreg.Register(name, func() time.Duration { return 10 * time.Second }, false, nil)
		// Deliberately never stamp — WithLabelValues alone creates the
		// series at 0, matching a task that has never once succeeded.
		metrics.LeaderTaskLastSuccess.WithLabelValues(name)

		w := doSystemTasksGET(router)
		row := findTaskView(t, w, name)
		if got := row["state"]; got != "overdue" {
			t.Errorf("state for a never-succeeded task with 100s uptime and 10s interval = %v, want overdue (100 > 2*10)", got)
		}
	})

	t.Run("long_interval_never_succeeded_is_ok", func(t *testing.T) {
		const name = "systest-never-succeeded-ok"
		taskreg.Register(name, func() time.Duration { return time.Hour }, false, nil)
		metrics.LeaderTaskLastSuccess.WithLabelValues(name)

		w := doSystemTasksGET(router)
		row := findTaskView(t, w, name)
		if got := row["state"]; got != "ok" {
			t.Errorf("state for a never-succeeded task with 100s uptime and 1h interval = %v, want ok (100 < 2*3600)", got)
		}
	})
}

// TestSystemTasks_NewLeaderGracePeriod is the B-F5 oracle: a leader-only
// task that has never stamped on THIS replica must be judged against
// uptime measured from when this replica most recently WON the leader
// lease, not from process start — a replica that has been running for
// days as a follower and only just became leader must not report a 24h
// task overdue for up to 24h.
func TestSystemTasks_NewLeaderGracePeriod(t *testing.T) {
	prevLeader := common.IsLeader()
	t.Cleanup(func() { common.SetLeader(prevLeader) })
	common.SetLeader(false)
	common.SetLeader(true) // false->true edge: stamps common.LeaderSince() to "now" (real wall clock)
	leaderSince := common.LeaderSince()
	if leaderSince == 0 {
		t.Fatalf("common.LeaderSince() = 0 right after SetLeader(true)")
	}

	// This replica has been up for 10 days (long before it became leader),
	// so judging the never-stamped task from common.StartTime alone would
	// read overdue for a 24h-interval task. Judging from leaderSince — 60s
	// "since winning the lease" — must read ok.
	prevStart := common.StartTime
	common.StartTime = leaderSince - 10*24*3600
	t.Cleanup(func() { common.StartTime = prevStart })

	prevClock := systemTasksNow
	fakeNow := leaderSince + 60
	systemTasksNow = func() int64 { return fakeNow }
	t.Cleanup(func() { systemTasksNow = prevClock })

	const name = "systest-new-leader-grace"
	taskreg.Register(name, func() time.Duration { return 24 * time.Hour }, true, nil) // leaderOnly, 24h interval
	metrics.LeaderTaskLastSuccess.WithLabelValues(name)                               // never stamped on this replica

	router := setupSystemTasksRootRouter(t, common.RoleRootUser)
	w := doSystemTasksGET(router)
	row := findTaskView(t, w, name)
	if got := row["state"]; got != "ok" {
		t.Errorf("newly-leading replica, never-stamped 24h leader-only task, 60s since winning the lease: state = %v, want ok (not overdue for a full day)", got)
	}
}

// TestSystemTasks_StandbyOnFollower is the direct oracle for the operator's
// binding ternary decision: a leader-gated task on a non-leader replica
// (last_success_at==0 because it has genuinely never been this process's job
// to run) must read "standby", never "overdue" — even though 0 has been
// "stale" for the entire process uptime, which is exactly the condition the
// non-leader-gated overdue branch would otherwise flag.
func TestSystemTasks_StandbyOnFollower(t *testing.T) {
	const name = "systest-standby-follower"
	taskreg.Register(name, func() time.Duration { return time.Second }, true, nil) // leaderOnly=true, tiny interval
	// Deliberately do NOT stamp the gauge — WithLabelValues alone creates the
	// series at 0, matching a follower that has never run this task.
	metrics.LeaderTaskLastSuccess.WithLabelValues(name)

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	router := setupSystemTasksRootRouter(t, common.RoleRootUser)
	w := doSystemTasksGET(router)
	row := findTaskView(t, w, name)
	if got := row["state"]; got != "standby" {
		t.Errorf("leader_only task on a non-leader replica: state = %v, want standby (not overdue)", got)
	}
	if got, _ := row["leader_only"].(bool); !got {
		t.Errorf("leader_only = false, want true")
	}
	if got := row["standby_reason"]; got != "follower" {
		t.Errorf("standby_reason = %v, want \"follower\"", got)
	}
}

// TestSystemTasks_DisabledJobIsNotOverdue is the B-F1 oracle: a task
// registered with a non-nil Active func that currently returns false must
// read "standby" with reason "disabled", never "overdue" — the state a
// naive nil-Active read would produce once uptime crosses 2x the nominal
// interval. Mirrors channel-health-test's real registration shape
// (interval from mutable settings, active gated on AutoTestChannelEnabled)
// without depending on operation_setting from this package's test binary.
// Mutation target: dropping the `!active` case in GetSystemTasksV2 turns
// this red (falls through to the uptime branch and reads overdue).
func TestSystemTasks_DisabledJobIsNotOverdue(t *testing.T) {
	const fixedNow int64 = 2_000_000_000
	prevClock := systemTasksNow
	systemTasksNow = func() int64 { return fixedNow }
	t.Cleanup(func() { systemTasksNow = prevClock })

	prevStart := common.StartTime
	// Uptime far beyond 2x the (tiny) interval below — if Active were
	// ignored, this alone would be enough to read overdue.
	common.StartTime = fixedNow - 1000
	t.Cleanup(func() { common.StartTime = prevStart })

	const name = "systest-disabled-job"
	taskreg.Register(name, func() time.Duration { return time.Second }, false, func() bool { return false })
	metrics.LeaderTaskLastSuccess.WithLabelValues(name) // never stamped, matching a job that never ran

	router := setupSystemTasksRootRouter(t, common.RoleRootUser)
	w := doSystemTasksGET(router)
	row := findTaskView(t, w, name)
	if got := row["state"]; got != "standby" {
		t.Errorf("administratively-disabled task: state = %v, want standby (never overdue)", got)
	}
	if got := row["standby_reason"]; got != "disabled" {
		t.Errorf("standby_reason = %v, want \"disabled\"", got)
	}
}
