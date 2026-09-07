package handler

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// V2 Log Stat Controller Tests
// ============================================================================

// seedStatLog creates a consume log with quota + token counts at a chosen
// timestamp so the aggregate and the rolling RPM/TPM window can both be tested.
// other is variadic (optional Other JSON, e.g. `{"source_product":"switch"}`)
// so every existing call site keeps compiling unchanged.
func seedStatLog(t *testing.T, ctx *V2TestContext, userID int, model string, quota, prompt, completion int, createdAt int64, other ...string) {
	t.Helper()
	otherJSON := ""
	if len(other) > 0 {
		otherJSON = other[0]
	}
	lg := &repo.Log{
		UserId:           userID,
		TenantId:         ctx.TenantID,
		Type:             repo.LogTypeConsume,
		ModelName:        model,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
		Other:            otherJSON,
	}
	if err := ctx.DB.Create(lg).Error; err != nil {
		t.Fatalf("failed to seed stat log: %v", err)
	}
}

func TestGetLogStatV2_AggregatesTotals(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 500, 50, 60, now)
	// Different user — must be excluded by the user scope.
	seedStatLog(t, ctx, ctx.AdminUser.Id, "gpt-4o", 9999, 9, 9, now)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat", nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if got := int(data["total_requests"].(float64)); got != 2 {
		t.Errorf("expected total_requests=2, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 1500 {
		t.Errorf("expected total_quota=1500, got %d", got)
	}
	if got := int(data["prompt_tokens"].(float64)); got != 150 {
		t.Errorf("expected prompt_tokens=150, got %d", got)
	}
	if got := int(data["completion_tokens"].(float64)); got != 260 {
		t.Errorf("expected completion_tokens=260, got %d", got)
	}
}

func TestGetLogStatV2_ModelFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)
	seedStatLog(t, ctx, ctx.NormalUser.Id, "claude-3.5", 500, 50, 60, now)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat?model_name=gpt-4o", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	if got := int(data["total_requests"].(float64)); got != 1 {
		t.Errorf("expected total_requests=1 for model filter, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 1000 {
		t.Errorf("expected total_quota=1000 for model filter, got %d", got)
	}
}

func TestGetLogStatV2_RpmTpmRollingWindow(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	// Recent: counts toward RPM/TPM and the all-time totals.
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)
	// Old (2 hours ago): counts toward totals but NOT the 60s RPM/TPM window.
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 500, 40, 60, now-7200)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	if got := int(data["total_requests"].(float64)); got != 2 {
		t.Errorf("expected total_requests=2 (both rows), got %d", got)
	}
	if got := int(data["rpm"].(float64)); got != 1 {
		t.Errorf("expected rpm=1 (only the recent row), got %d", got)
	}
	if got := int(data["tpm"].(float64)); got != 300 {
		t.Errorf("expected tpm=300 (100+200 of recent row), got %d", got)
	}
}

func TestGetLogStatV2_EmptyWindow(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)

	// start_time strictly in the future → no rows match the window totals.
	path := "/api/v2/test-tenant/logs/stat?start_time=" + strconv.FormatInt(now+3600, 10)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	if got := int(data["total_requests"].(float64)); got != 0 {
		t.Errorf("expected total_requests=0 for empty window, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 0 {
		t.Errorf("expected total_quota=0 for empty window, got %d", got)
	}
}

func TestGetLogStatV2_WideWindowReturnsAllTotals(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now-86400*200) // ~200 days ago
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 500, 50, 60, now)

	// A very wide explicit window must still aggregate both rows as one SUM.
	path := "/api/v2/test-tenant/logs/stat?start_time=1&end_time=" + strconv.FormatInt(now+3600, 10)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	if got := int(data["total_requests"].(float64)); got != 2 {
		t.Errorf("expected total_requests=2 across wide window, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 1500 {
		t.Errorf("expected total_quota=1500 across wide window, got %d", got)
	}
}

// TestGetAllLogStatV2_AdminSeesTenantWideTotals: /logs/stat/all drops the
// user_id scope so the header matches the rows /logs/all lists. The same
// admin on plain /logs/stat still sees only their own rows — that contrast is
// what a "route both paths to the same handler" mutation would break.
func TestGetAllLogStatV2_AdminSeesTenantWideTotals(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)
	seedStatLog(t, ctx, ctx.NormalUser.Id, "claude-3.5", 500, 50, 60, now)
	seedStatLog(t, ctx, ctx.AdminUser.Id, "gpt-4o", 250, 25, 30, now)

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/stat/all", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["total_requests"].(float64)); got != 3 {
		t.Errorf("expected tenant-wide total_requests=3, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 1750 {
		t.Errorf("expected tenant-wide total_quota=1750, got %d", got)
	}
	if got := int(data["tpm"].(float64)); got != 465 {
		t.Errorf("expected tenant-wide tpm=465, got %d", got)
	}

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/stat", nil, nil)
	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	if got := int(data["total_requests"].(float64)); got != 1 {
		t.Errorf("expected caller-scoped total_requests=1 on /logs/stat, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 250 {
		t.Errorf("expected caller-scoped total_quota=250 on /logs/stat, got %d", got)
	}
}

// TestGetAllLogStatV2_ForbiddenForNormalUser: tenant-wide aggregates cover
// every member's usage, so the route carries the same admin gate as /logs/all.
func TestGetAllLogStatV2_ForbiddenForNormalUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat/all", nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)
}

// TestGetAllLogStatV2_UsernameFilter: the tenant-wide route accepts the same
// `username` member filter GetAllLogsV2 does.
func TestGetAllLogStatV2_UsernameFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	for _, row := range []struct {
		userID   int
		username string
		quota    int
	}{
		{ctx.NormalUser.Id, "alice", 1000},
		{ctx.NormalUser.Id, "alice", 200},
		{ctx.AdminUser.Id, "bob", 9999},
	} {
		lg := &repo.Log{
			UserId:    row.userID,
			TenantId:  ctx.TenantID,
			Type:      repo.LogTypeConsume,
			Username:  row.username,
			ModelName: "gpt-4o",
			Quota:     row.quota,
			CreatedAt: now,
		}
		if err := ctx.DB.Create(lg).Error; err != nil {
			t.Fatalf("failed to seed log: %v", err)
		}
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/stat/all?username=alice", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["total_requests"].(float64)); got != 2 {
		t.Errorf("expected total_requests=2 for username filter, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 1200 {
		t.Errorf("expected total_quota=1200 for username filter, got %d", got)
	}
}

// TestGetLogStatV2_SourceProductFilterAndBreakdown covers both new pieces of
// Workstream 0 on this endpoint: the source_product filter narrows the
// header totals, and by_product always shows the FULL breakdown of the
// window regardless of that filter — so a caller who filters to "switch"
// still sees where the rest of the window's spend (kova) went.
func TestGetLogStatV2_SourceProductFilterAndBreakdown(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 30, 3, 6, now, `{"source_product":"switch"}`)
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 20, 2, 4, now, `{"source_product":"switch"}`)
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 5, 1, 1, now, `{"source_product":"kova"}`)

	// Filtered: totals narrow to the two switch rows.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat?source_product=switch", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["total_requests"].(float64)); got != 2 {
		t.Errorf("expected total_requests=2 for source_product=switch, got %d", got)
	}
	if got := int(data["total_quota"].(float64)); got != 50 {
		t.Errorf("expected total_quota=50 for source_product=switch, got %d", got)
	}

	byProduct, ok := data["by_product"].([]interface{})
	if !ok {
		t.Fatalf("by_product missing or wrong type: %v", data["by_product"])
	}
	found := map[string]int{}
	for _, row := range byProduct {
		m := row.(map[string]interface{})
		found[m["source_product"].(string)] = int(m["total_quota"].(float64))
	}
	if found["switch"] != 50 {
		t.Errorf("by_product[switch].total_quota = %d, want 50", found["switch"])
	}
	if found["kova"] != 5 {
		t.Errorf("by_product[kova].total_quota = %d, want 5 — filtering the totals must not hide "+
			"the rest of the window from the breakdown", found["kova"])
	}

	// Unfiltered: totals cover all three rows.
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat", nil, nil)
	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	if got := int(data["total_quota"].(float64)); got != 55 {
		t.Errorf("expected unfiltered total_quota=55, got %d", got)
	}
}
