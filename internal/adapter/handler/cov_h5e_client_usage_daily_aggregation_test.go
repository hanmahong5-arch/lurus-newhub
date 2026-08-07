package handler

// cov_h5e_client_usage_daily_aggregation_test.go — business-acceptance
// coverage for v2_client_api.go's ClientGetUsageDaily aggregation loop,
// which the existing default/custom/max-days tests never exercise because
// they run against an empty quota_data table (every day in the response
// window is the "no data" zero entry). This seeds TWO quota_data rows on
// the SAME day (so both the map-miss "create bucket" branch and the
// map-hit "accumulate into existing bucket" branch run) plus verifies the
// per-day totals — proving real accumulation, not just presence.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func TestClientGetUsageDaily_MultipleRowsSameDay_AggregatesCorrectly(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	rows := []repo.QuotaData{
		{UserID: ctx.NormalUser.Id, ModelName: "gpt-4o", CreatedAt: todayStart, Quota: 100, TokenUsed: 50, Count: 1},
		{UserID: ctx.NormalUser.Id, ModelName: "gpt-3.5", CreatedAt: todayStart, Quota: 200, TokenUsed: 75, Count: 2},
		// A different user's row on the same day must NOT leak into this user's total.
		{UserID: ctx.AdminUser.Id, ModelName: "gpt-4o", CreatedAt: todayStart, Quota: 9999, TokenUsed: 9999, Count: 99},
	}
	for i := range rows {
		if err := ctx.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed quota_data row %d: %v", i, err)
		}
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/usage/daily?days=1", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	daily := data["daily"].([]interface{})
	if len(daily) != 1 {
		t.Fatalf("len(daily) = %d, want 1 (days=1)", len(daily))
	}
	entry := daily[0].(map[string]interface{})

	wantDate := now.Format("2006-01-02")
	if entry["date"] != wantDate {
		t.Errorf("date = %v, want %v", entry["date"], wantDate)
	}
	if got := int(entry["quota"].(float64)); got != 300 {
		t.Errorf("quota = %d, want 300 (100+200, accumulated across two same-day rows)", got)
	}
	if got := int(entry["token_used"].(float64)); got != 125 {
		t.Errorf("token_used = %d, want 125 (50+75)", got)
	}
	if got := int(entry["count"].(float64)); got != 3 {
		t.Errorf("count = %d, want 3 (1+2)", got)
	}
}
