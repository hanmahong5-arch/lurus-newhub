package handler

// task_test.go — cycle-8 L10 oracles for the project_id/request_id filters
// GetAllTask (admin) and GetUserTask (self) gained on top of migration 035's
// tasks.project_id/tasks.request_id columns (L8) and the repo.Task query
// builders those two handlers already fed (also L8). Reuses the
// SetupV2TestRouter + r2deplCtx/r2deplBody helpers already established for
// this package's DB-backed handler tests (cover_r2_deploy_test.go).

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// TestNormalizeRequestIDFilter is the direct unit oracle for
// normalizeRequestIDFilter (cycle-8 L10 repair, A-F1): reducing the function
// to `return raw, false` — the mutation that left the handler-level "capped"
// subtest green before this repair — turns this red on both cases: the
// padded value's whitespace would survive, and the oversized value would
// come back valid instead of invalid.
func TestNormalizeRequestIDFilter(t *testing.T) {
	if v, invalid := normalizeRequestIDFilter("  req-x  "); v != "req-x" || invalid {
		t.Errorf("normalizeRequestIDFilter(padded) = (%q, %v), want (\"req-x\", false)", v, invalid)
	}
	long := strings.Repeat("a", 100)
	if v, invalid := normalizeRequestIDFilter(long); v != "" || !invalid {
		t.Errorf("normalizeRequestIDFilter(100 chars) = (%q, %v), want (\"\", true)", v, invalid)
	}
	exact64 := strings.Repeat("c", 64)
	if v, invalid := normalizeRequestIDFilter(exact64); v != exact64 || invalid {
		t.Errorf("normalizeRequestIDFilter(exactly 64 chars) = (%q, %v), want (%q, false)", v, invalid, exact64)
	}
	if v, invalid := normalizeRequestIDFilter(""); v != "" || invalid {
		t.Errorf("normalizeRequestIDFilter(\"\") = (%q, %v), want (\"\", false)", v, invalid)
	}
}

// TestNormalizeProjectIDFilter is the direct unit oracle for
// normalizeProjectIDFilter (cycle-8 L10 repair, B-F1).
func TestNormalizeProjectIDFilter(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		invalid bool
	}{
		{"", "", false},
		{"5", "5", false},
		{"  7  ", "7", false},
		{"abc", "", true},
		{"-3", "", true},
		{"0", "", true},
	}
	for _, tc := range cases {
		v, invalid := normalizeProjectIDFilter(tc.raw)
		if v != tc.want || invalid != tc.invalid {
			t.Errorf("normalizeProjectIDFilter(%q) = (%q, %v), want (%q, %v)", tc.raw, v, invalid, tc.want, tc.invalid)
		}
	}
}

func TestGetAllTask_FiltersByProjectAndRequestId(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate Task: %v", err)
	}

	seed := []*repo.Task{
		{TaskID: "adm-match", Platform: constant.TaskPlatformSuno, UserId: ctx.NormalUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 5, RequestId: "req-admin-match", CreatedAt: time.Now().Unix()},
		{TaskID: "adm-other", Platform: constant.TaskPlatformSuno, UserId: ctx.NormalUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 9, RequestId: "req-admin-other", CreatedAt: time.Now().Unix()},
	}
	for _, s := range seed {
		if err := ctx.DB.Create(s).Error; err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}

	t.Run("project_id narrows the admin list and its total", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&project_id=5", nil)
		c.Set("role", common.RoleRootUser) // admin list, not tenant-scoping under test
		GetAllTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("expected data; body=%s", w.Body.String())
		}
		if total, _ := data["total"].(float64); total != 1 {
			t.Errorf("total = %v, want 1", data["total"])
		}
		items, _ := data["items"].([]interface{})
		if len(items) != 1 {
			t.Fatalf("items = %v, want exactly 1", items)
		}
		row, _ := items[0].(map[string]interface{})
		if row["task_id"] != "adm-match" {
			t.Errorf("task_id = %v, want adm-match", row["task_id"])
		}
		if row["request_id"] != "req-admin-match" {
			t.Errorf("request_id = %v, want req-admin-match (must be exposed on the row)", row["request_id"])
		}
	})

	t.Run("request_id narrows the admin list", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&request_id=req-admin-other", nil)
		c.Set("role", common.RoleRootUser) // admin list, not tenant-scoping under test
		GetAllTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 1 {
			t.Errorf("total = %v, want 1", data["total"])
		}
		items, _ := data["items"].([]interface{})
		if len(items) != 1 {
			t.Fatalf("items = %v, want exactly 1", items)
		}
		row, _ := items[0].(map[string]interface{})
		if row["task_id"] != "adm-other" {
			t.Errorf("task_id = %v, want adm-other", row["task_id"])
		}
	})

	// A-F1: without TrimSpace, a leading/trailing-space request_id would
	// never equal the stored (untrimmed) value and this goes red.
	t.Run("a padded request_id is trimmed before the query", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&request_id=%20req-admin-other%20", nil)
		c.Set("role", common.RoleRootUser) // admin list, not tenant-scoping under test
		GetAllTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 1 {
			t.Errorf("total = %v, want 1", data["total"])
		}
	})

	t.Run("an oversized request_id yields the empty page instead of being truncated into a match", func(t *testing.T) {
		long := ""
		for i := 0; i < 100; i++ {
			long += "a"
		}
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&request_id="+long, nil)
		c.Set("role", common.RoleRootUser) // admin list, not tenant-scoping under test
		GetAllTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 0 {
			t.Errorf("total = %v, want 0 (no task has a 100-char request_id)", data["total"])
		}
	})

	// B-F2: byte-truncating an oversized request_id to 64 chars (the earlier
	// version of normalizeRequestIDFilter) can accidentally MATCH a stored
	// 64-char id when the query value's first 64 bytes happen to equal it —
	// the row seeded below ("adm-64char") has a request_id that is exactly
	// 64 chars, and this query sends its 70-char superset. Truncation would
	// find it (total 1, wrong); rejecting an oversized filter outright must
	// report 0.
	t.Run("a 70-char request_id whose 64-char prefix matches a stored id still yields empty, not a truncated match", func(t *testing.T) {
		stored64 := strings.Repeat("b", 64)
		if err := ctx.DB.Create(&repo.Task{TaskID: "adm-64char", Platform: constant.TaskPlatformSuno, UserId: ctx.NormalUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", RequestId: stored64, CreatedAt: time.Now().Unix()}).Error; err != nil {
			t.Fatalf("seed 64-char request_id task: %v", err)
		}
		query := stored64 + "extra6" // 64 + 6 = 70 chars, first 64 equal stored64
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&request_id="+query, nil)
		c.Set("role", common.RoleRootUser) // admin list, not tenant-scoping under test
		GetAllTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 0 {
			t.Errorf("total = %v, want 0 — an oversized filter must not truncate into a false match", data["total"])
		}
	})

	// B-F1: project_id is forwarded into an integer column (project_id = ?).
	// A non-numeric value must short-circuit to the empty page instead of
	// reaching the query builder, where on PG it raises 22P02 and the
	// builder's nil-on-error swallow turns that into items:null plus a
	// per-request error log — not the same thing as a filtered empty page.
	t.Run("a non-numeric project_id yields the empty page without an items:null response", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&project_id=abc", nil)
		c.Set("role", common.RoleRootUser) // admin list, not tenant-scoping under test
		GetAllTask(c)
		body := r2deplBody(t, w)
		if ok, _ := body["success"].(bool); !ok {
			t.Fatalf("expected success=true; body=%s", w.Body.String())
		}
		data, _ := body["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 0 {
			t.Errorf("total = %v, want 0", data["total"])
		}
		items, ok := data["items"].([]interface{})
		if !ok {
			t.Fatalf("items = %v (%T), want an empty array (not null)", data["items"], data["items"])
		}
		if len(items) != 0 {
			t.Errorf("items = %v, want empty", items)
		}
	})
}

func TestGetUserTask_ForeignProjectFilterIsEmpty(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate Task: %v", err)
	}

	// NormalUser owns a task under project 5; project 999 belongs to nobody
	// this user can see (e.g. another tenant's project).
	seed := []*repo.Task{
		{TaskID: "own-1", Platform: constant.TaskPlatformSuno, UserId: ctx.NormalUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 5, RequestId: "req-own-1", CreatedAt: time.Now().Unix()},
		{TaskID: "other-user-999", Platform: constant.TaskPlatformSuno, UserId: 99999, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", ProjectId: 999, RequestId: "req-other-999", CreatedAt: time.Now().Unix()},
	}
	for _, s := range seed {
		if err := ctx.DB.Create(s).Error; err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}

	t.Run("own project_id returns the row", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&project_id=5", nil)
		c.Set("id", ctx.NormalUser.Id)
		GetUserTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 1 {
			t.Errorf("total = %v, want 1", data["total"])
		}
	})

	t.Run("a foreign project_id yields an empty page, not an error or a 403", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&project_id=999", nil)
		c.Set("id", ctx.NormalUser.Id)
		GetUserTask(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (fail-closed = empty page, not an HTTP error)", w.Code)
		}
		body := r2deplBody(t, w)
		if ok, _ := body["success"].(bool); !ok {
			t.Fatalf("expected success=true; body=%s", w.Body.String())
		}
		data, _ := body["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 0 {
			t.Errorf("total = %v, want 0 — project 999 is not this user's, must not leak its task", data["total"])
		}
		items, _ := data["items"].([]interface{})
		if len(items) != 0 {
			t.Errorf("items = %v, want empty", items)
		}
	})

	t.Run("request_id is scoped by user_id too", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10&request_id=req-other-999", nil)
		c.Set("id", ctx.NormalUser.Id)
		GetUserTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if total, _ := data["total"].(float64); total != 0 {
			t.Errorf("total = %v, want 0 — that request_id belongs to another user's task", data["total"])
		}
	})
}
