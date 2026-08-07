package handler

// cov_h5e_client_api_edges_test.go — business-acceptance coverage for the
// remaining gaps in v2_client_api.go: the 404 "user not found" branches on
// ClientGetProfile/ClientGetSessions (the authenticated-user id no longer
// resolves to a live row — e.g. deleted between token issuance and use),
// ClientGetUsageDaily's days<1 clamp and its genuine DB-failure branch, and
// ClientGetTokens' page/page_size clamps for non-positive/oversized values.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestClientGetProfile_UnknownUserID_404 exercises the branch where the
// authenticated id (set by FlexAuth) no longer has a corresponding user row.
func TestClientGetProfile_UnknownUserID_404(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/profile", nil, map[string]string{
		"X-Test-User-ID": "987654321",
	})
	AssertV2Status(t, w, http.StatusNotFound)
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Errorf("success = %v, want false", resp["success"])
	}
	if resp["message"] != "user not found" {
		t.Errorf("message = %v, want 'user not found'", resp["message"])
	}
}

// TestClientGetSessions_UnknownUserID_404 mirrors the profile 404 for the
// sessions endpoint's own independent GetUserById call.
func TestClientGetSessions_UnknownUserID_404(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/sessions", nil, map[string]string{
		"X-Test-User-ID": "987654322",
	})
	AssertV2Status(t, w, http.StatusNotFound)
	resp := handlerDeployParseBody(t, w)
	if resp["message"] != "user not found" {
		t.Errorf("message = %v, want 'user not found'", resp["message"])
	}
}

// TestClientGetUsageDaily_ZeroDays_ClampsToDefault30 covers the days<1
// clamp: days=0 must behave exactly like the (already-tested) default,
// i.e. resolve to 30, not 0 or a negative range.
func TestClientGetUsageDaily_ZeroDays_ClampsToDefault30(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/usage/daily?days=0", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["days"].(float64)); got != 30 {
		t.Errorf("days = %d, want 30 (days=0 clamps to default)", got)
	}
	daily := data["daily"].([]interface{})
	if len(daily) != 30 {
		t.Errorf("len(daily) = %d, want 30", len(daily))
	}
}

// TestClientGetUsageDaily_NegativeDays_ClampsToDefault30 covers the same
// clamp for a genuinely negative value (not just zero).
func TestClientGetUsageDaily_NegativeDays_ClampsToDefault30(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/usage/daily?days=-5", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["days"].(float64)); got != 30 {
		t.Errorf("days = %d, want 30 (days=-5 clamps to default)", got)
	}
}

// TestClientGetUsageDaily_QuotaDataTableMissing_500 forces the genuine DB
// failure branch: repo.GetQuotaDataByUserId errors when its backing table
// is gone, and the handler must surface a 500, not silently return an
// empty daily series.
func TestClientGetUsageDaily_QuotaDataTableMissing_500(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.QuotaData{}); err != nil {
		t.Fatalf("drop quota_data table: %v", err)
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/usage/daily", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusInternalServerError)
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Errorf("success = %v, want false", resp["success"])
	}
	if resp["message"] != "failed to query usage data" {
		t.Errorf("message = %v, want 'failed to query usage data'", resp["message"])
	}
}

// TestClientGetTokens_NonPositivePage_ClampsToPage1 covers page<1: p=0 must
// resolve to page 1's window, not an empty/negative offset.
func TestClientGetTokens_NonPositivePage_ClampsToPage1(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "clamp-token-1")

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/tokens?p=0", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["page"].(float64)); got != 1 {
		t.Errorf("page = %d, want 1 (p=0 clamps to page 1)", got)
	}
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("expected the seeded token to be visible on the clamped page 1, got %d items", len(items))
	}
}

// TestClientGetTokens_OversizedPageSize_ClampsTo20 covers pageSize>100:
// size=500 must clamp to the 20 default, not return an unbounded page.
func TestClientGetTokens_OversizedPageSize_ClampsTo20(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()
	for i := 0; i < 25; i++ {
		SeedV2Token(t, ctx, ctx.NormalUser.Id, fmt.Sprintf("clamp-oversized-%d", i))
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/tokens?size=500", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["page_size"].(float64)); got != 20 {
		t.Errorf("page_size = %d, want 20 (size=500 clamps to default)", got)
	}
	items := data["items"].([]interface{})
	if len(items) != 20 {
		t.Errorf("expected exactly 20 items on the clamped page, got %d", len(items))
	}
}

// TestClientGetTokens_ZeroPageSize_ClampsTo20 covers pageSize<1 (as
// distinct from the >100 branch above — both land on the same reset, but
// zero exercises the OTHER side of the guard's ||).
func TestClientGetTokens_ZeroPageSize_ClampsTo20(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "clamp-zero-size")

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/tokens?size=0", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["page_size"].(float64)); got != 20 {
		t.Errorf("page_size = %d, want 20 (size=0 clamps to default)", got)
	}
}
