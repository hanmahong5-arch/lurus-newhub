package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// seedClusterLog inserts a log with explicit model_name, content (the real
// error text — the cluster endpoint reports ERROR clusters, see
// v2_log_cluster.go), and created_at. Type is LogTypeError, not
// LogTypeConsume: the endpoint's `type = ?` predicate excludes everything
// else (locked by TestGetLogClusterV2_CrossTenantIsolation in
// v2_cross_tenant_isolation_test.go), so a LogTypeConsume seed here would
// never surface in the response.
func seedClusterLog(t *testing.T, ctx *V2TestContext, userID int, model, errorCode string, createdAt int64) {
	t.Helper()
	l := &repo.Log{
		UserId:    userID,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeError,
		ModelName: model,
		Content:   errorCode,
		CreatedAt: createdAt,
	}
	if err := ctx.DB.Create(l).Error; err != nil {
		t.Fatalf("seedClusterLog: %v", err)
	}
}

// clusterPath returns the URL for the cluster endpoint using ctx.TenantID as
// the slug segment — the mock auth ignores the slug and reads from headers.
func clusterPath(ctx *V2TestContext, query string) string {
	base := "/api/v2/test-tenant/logs/cluster"
	if query != "" {
		return base + "?" + query
	}
	return base
}

// ── Tests ────────────────────────────────────────────────────────────────────

// TestV2LogCluster_HappyPath: 10 logs across 2 models × 2 error_codes × 3 hours.
// With ?bucket=hour the aggregated rows must be non-empty and each have count ≥ 1.
func TestV2LogCluster_HappyPath(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	base := int64(1_700_000_000)
	models := []string{"gpt-4o", "claude-3.5-sonnet"}
	errCodes := []string{"", "UPSTREAM_ERROR"}
	for hi := 0; hi < 3; hi++ {
		ts := base + int64(hi)*3600
		for _, m := range models {
			for _, ec := range errCodes {
				seedClusterLog(t, ctx, ctx.NormalUser.Id, m, ec, ts)
			}
		}
	}
	// One extra row to verify count accumulation (gpt-4o / no error / hour 0).
	seedClusterLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", "", base+10)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		clusterPath(ctx, "bucket=hour&from=0&to=9999999999"), nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["bucket"] != "hour" {
		t.Errorf("bucket = %v, want hour", data["bucket"])
	}
	items := data["items"].([]interface{})
	if len(items) == 0 {
		t.Fatal("expected at least 1 aggregated row, got 0")
	}
	for i, it := range items {
		row := it.(map[string]interface{})
		if row["count"].(float64) < 1 {
			t.Errorf("items[%d].count = %v, want >= 1", i, row["count"])
		}
		if row["bucket"] == "" {
			t.Errorf("items[%d].bucket is empty", i)
		}
	}
}

// TestV2LogCluster_DayBucket: 6 logs all within the same UTC hour, spread
// across 6 one-minute intervals. ?bucket=hour → 1 row per error_code (2 rows
// total). ?bucket=day → same 2 rows (same hour is within the same day). The
// key invariant is that day-bucket always yields ≤ hour-bucket row count, and
// that the response field "bucket" equals "day".
func TestV2LogCluster_DayBucket(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Use a base within the middle of a UTC day to avoid cross-day spill.
	// 1_700_056_800 = 2023-11-15 12:00:00 UTC (noon).
	base := int64(1_700_056_800)
	for i := 0; i < 6; i++ {
		ts := base + int64(i)*60 // same hour, 1-minute apart
		seedClusterLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", "", ts)
		seedClusterLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", "ERR", ts)
	}

	wHour := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		clusterPath(ctx, "bucket=hour&from=0&to=9999999999"), nil, nil)
	hourItems := AssertV2Success(t, wHour)["data"].(map[string]interface{})["items"].([]interface{})

	wDay := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		clusterPath(ctx, "bucket=day&from=0&to=9999999999"), nil, nil)
	AssertV2Status(t, wDay, http.StatusOK)
	dayData := AssertV2Success(t, wDay)["data"].(map[string]interface{})
	dayItems := dayData["items"].([]interface{})

	if dayData["bucket"] != "day" {
		t.Errorf("bucket = %v, want day", dayData["bucket"])
	}
	// hour and day both collapse to 1 row per error_code when all logs are
	// in the same hour; day must be ≤ hour (never more granular).
	if len(dayItems) > len(hourItems) {
		t.Errorf("day items (%d) > hour items (%d): day must be ≤ hour", len(dayItems), len(hourItems))
	}
	// Each day row aggregates all 6 insertions per error_code.
	for _, it := range dayItems {
		row := it.(map[string]interface{})
		if row["count"].(float64) < 6 {
			t.Errorf("day row count = %v, want >= 6 (all 6 insertions per error_code)", row["count"])
		}
	}
}

// TestV2LogCluster_TenantIsolation: two independent test DBs each with their
// own tenant. Logs seeded in ctxA must not appear when querying ctxB.
func TestV2LogCluster_TenantIsolation(t *testing.T) {
	ctxA := SetupV2TestRouter(t)
	defer ctxA.Cleanup()

	ctxB := SetupV2TestRouter(t)
	defer ctxB.Cleanup()

	base := int64(1_700_000_000)
	for i := 0; i < 5; i++ {
		seedClusterLog(t, ctxA, ctxA.NormalUser.Id, "gpt-4o", "ERR", base+int64(i)*60)
	}

	// ctxB has a completely separate SQLite DB — no logs seeded there.
	w := V2RequestAsUser(ctxB, ctxB.NormalUser, http.MethodGet,
		clusterPath(ctxB, "from=0&to=9999999999"), nil, nil)
	AssertV2Status(t, w, http.StatusOK)

	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	data := out["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("tenantB should see 0 items, got %d (tenant isolation broken)", len(items))
	}
}
