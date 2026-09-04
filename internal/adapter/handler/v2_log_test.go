package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// ============================================================================
// V2 Log Controller Tests
// ============================================================================

func TestGetLogsV2_UserScope(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create logs for normal user
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeTopup)

	// Create logs for admin user
	SeedV2Log(t, ctx, ctx.AdminUser.Id, repo.LogTypeConsume)

	// Get logs as normal user - should only see their own
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs", nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 logs for normal user, got %d", total)
	}
}

func TestGetLogsV2_Filters(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create logs with different types
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeTopup)

	// Filter by type
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs?type="+strconv.Itoa(repo.LogTypeConsume), nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 consume logs, got %d", total)
	}
}

func TestGetLogsV2_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create 25 logs
	for i := 0; i < 25; i++ {
		SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	}

	// Get first page
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs?page=1&page_size=10", nil, nil)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	logs := data["logs"].([]interface{})
	if len(logs) != 10 {
		t.Errorf("expected 10 logs on first page, got %d", len(logs))
	}

	total := int(data["total"].(float64))
	if total != 25 {
		t.Errorf("expected total=25, got %d", total)
	}

	// Get third page
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs?page=3&page_size=10", nil, nil)
	resp = AssertV2Success(t, w)

	data = resp["data"].(map[string]interface{})
	logs = data["logs"].([]interface{})
	if len(logs) != 5 {
		t.Errorf("expected 5 logs on third page, got %d", len(logs))
	}
}

func TestGetAllLogsV2_TenantScope(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create logs for both users in the same tenant
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeTopup)
	SeedV2Log(t, ctx, ctx.AdminUser.Id, repo.LogTypeConsume)

	// Create a log in a different tenant (should not be included)
	otherTenantLog := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  "other-tenant",
		Type:      repo.LogTypeConsume,
		Content:   "Other tenant log",
		CreatedAt: 0,
	}
	ctx.DB.Create(otherTenantLog)

	// Get all logs (admin endpoint, gets all logs in tenant)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/all", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 3 {
		t.Errorf("expected 3 logs in tenant, got %d", total)
	}
}

func TestGetLogsV2_DateRangeFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create logs with different timestamps
	now := int64(1700000000) // Fixed timestamp for testing

	log1 := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeConsume,
		Content:   "Old log",
		CreatedAt: now - 86400*2, // 2 days ago
	}
	log2 := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeConsume,
		Content:   "Recent log",
		CreatedAt: now - 3600, // 1 hour ago
	}
	log3 := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeConsume,
		Content:   "Very old log",
		CreatedAt: now - 86400*10, // 10 days ago
	}
	ctx.DB.Create(log1)
	ctx.DB.Create(log2)
	ctx.DB.Create(log3)

	// Filter by date range (last 3 days)
	startTime := now - 86400*3
	endTime := now

	path := "/api/v2/test-tenant/logs?start_time=" + strconv.FormatInt(startTime, 10) + "&end_time=" + strconv.FormatInt(endTime, 10)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 logs in date range, got %d", total)
	}
}

func TestGetLogsV2_EmptyResult(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Don't create any logs

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs", nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 0 {
		t.Errorf("expected 0 logs, got %d", total)
	}

	logs := data["logs"]
	if logs == nil {
		// Some implementations return nil for empty array
		return
	}
	if logsArr, ok := logs.([]interface{}); ok && len(logsArr) != 0 {
		t.Errorf("expected empty logs array, got %d items", len(logsArr))
	}
}

func TestGetAllLogsV2_WithFilters(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create logs with different types and users
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeTopup)
	SeedV2Log(t, ctx, ctx.AdminUser.Id, repo.LogTypeConsume)

	// Filter by type only
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/all?type="+strconv.Itoa(repo.LogTypeConsume), nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 consume logs, got %d", total)
	}
}

func TestGetAllLogsV2_NonAdminRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)

	// /logs/all returns every tenant member's logs (GetTenantLogsWithParams, no
	// user_id filter), so a non-admin tenant member must be rejected. The route is
	// mounted under UserAuth(); the admin gate is enforced in the handler via
	// requireTenantAdmin (see TestSecurityLogsAllRequiresAdmin for full coverage).
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/all", nil, nil)

	AssertV2Status(t, w, http.StatusForbidden)
}

// TestGetLogsV2_ForbiddenFields guards the logView whitelist: the log endpoints
// must not expose tenant_id, caller IP (PII), or the governance-internal
// fingerprint/upstream-model columns. `other` is tier-filtered rather than
// dropped (contract change 2026-08-31, matching the v1 self-log route): the
// user route keeps user-tier keys (cache_tokens, request_path) and strips
// every TierInternal key (admin_info, pricing ratios).
func TestGetLogsV2_ForbiddenFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	sensitive := &repo.Log{
		UserId:             ctx.NormalUser.Id,
		TenantId:           ctx.TenantID,
		Type:               repo.LogTypeConsume,
		Content:            "log with secrets",
		ModelName:          "gpt-4",
		CreatedAt:          1700000000,
		Ip:                 "203.0.113.7",
		Other:              `{"cache_tokens":42,"request_path":"/v1/chat/completions","model_ratio":2.5,"group_ratio":1.0,"frt":123.0,"admin_info":{"use_channel":["7"],"route_attempts":[{"channel_id":7}]}}`,
		RequestFingerprint: "fp-abc123",
		UpstreamModel:      "gpt-4-internal",
	}
	if err := ctx.DB.Create(sensitive).Error; err != nil {
		t.Fatalf("failed to seed sensitive log: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	logs := data["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	lg := logs[0].(map[string]interface{})

	for _, f := range []string{"tenant_id", "ip", "request_fingerprint", "upstream_model", "channel_type", "relay_mode"} {
		if _, exists := lg[f]; exists {
			t.Errorf("forbidden field %q leaked through logView", f)
		}
	}
	if _, ok := lg["model_name"]; !ok {
		t.Error("expected model_name field in logView")
	}

	other, ok := lg["other"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized `other` object on the user route, got %T", lg["other"])
	}
	if got, ok := other["cache_tokens"].(float64); !ok || got != 42 {
		t.Errorf("user-tier key cache_tokens should survive sanitization, got %v", other["cache_tokens"])
	}
	if got, ok := other["request_path"].(string); !ok || got != "/v1/chat/completions" {
		t.Errorf("user-tier key request_path should survive sanitization, got %v", other["request_path"])
	}
	// frt (time to first token) is the caller's own request timing, classified
	// TierPublic alongside total_latency_ms since 2026-09-01. It used to be
	// stripped here, which made the headline latency metric of a gateway the one
	// number the paying customer could not see.
	if got, ok := other["frt"].(float64); !ok || got != 123.0 {
		t.Errorf("frt should survive sanitization (TierPublic — the caller's own latency), got %v", other["frt"])
	}
	for _, f := range []string{"admin_info", "model_ratio", "group_ratio"} {
		if _, exists := other[f]; exists {
			t.Errorf("TierInternal key %q leaked through the user-route `other` projection", f)
		}
	}
}

// TestGetAllLogsV2_AdminSeesFullOther: the tenant-admin route ships the full
// `other` payload — admin_info.route_attempts is what feeds the console's
// routing-trace panel, which was structurally dead while v2 dropped `other`.
func TestGetAllLogsV2_AdminSeesFullOther(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	row := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeConsume,
		ModelName: "gpt-4",
		CreatedAt: 1700000000,
		Other:     `{"cache_tokens":7,"model_ratio":2.5,"admin_info":{"route_attempts":[{"channel_id":7,"outcome":"success"}]}}`,
	}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("failed to seed log: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/all", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	logs := data["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	other, ok := logs[0].(map[string]interface{})["other"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected full `other` object on the admin route")
	}
	if _, exists := other["model_ratio"]; !exists {
		t.Error("admin route must keep TierInternal pricing keys")
	}
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("admin route must keep admin_info, got %v", other["admin_info"])
	}
	if _, exists := adminInfo["route_attempts"]; !exists {
		t.Error("admin_info.route_attempts must reach the admin route (routing panel data)")
	}
}

// TestGetLogsV2_CorruptOtherOmitted: a corrupt stored payload must be omitted,
// not embedded — an invalid RawMessage would break marshalling of the whole
// response and 500 the page for every row in it.
func TestGetLogsV2_CorruptOtherOmitted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	row := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeConsume,
		ModelName: "gpt-4",
		CreatedAt: 1700000000,
		Other:     `{not-json`,
	}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("failed to seed log: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs", nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	logs := data["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if _, exists := logs[0].(map[string]interface{})["other"]; exists {
		t.Error("corrupt `other` must be omitted from the projection")
	}
}

// TestGetLogsV2_AfterIDCursor guards the live-tail cursor: requesting
// ?after_id=<id> returns only rows strictly newer than that id.
func TestGetLogsV2_AfterIDCursor(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed three logs; ids are monotonic in insertion order.
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	l2 := SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	l3 := SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)

	path := fmt.Sprintf("/api/v2/test-tenant/logs?after_id=%d", l2.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	logs := data["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log after id=%d, got %d", l2.Id, len(logs))
	}
	got := logs[0].(map[string]interface{})
	if int(got["id"].(float64)) != l3.Id {
		t.Errorf("expected newest log id=%d, got %v", l3.Id, got["id"])
	}
}

func TestGetLogsV2_TypeFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create logs with different types for the same user
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeTopup)

	// Filter by topup type on the user's own logs endpoint
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs?type="+strconv.Itoa(repo.LogTypeTopup), nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 topup log, got %d", total)
	}
}

// TestGetLogsV2_ChannelIdentityHiddenFromUser locks the 2026-09-03 decision
// that an ordinary customer must not read which upstream account served their
// request. The operator's channel naming ("openai-key3", "azure-backup")
// describes our supply chain, not the caller's request, and governance
// classifies channel_name/channel_id TierInternal.
//
// It has to cover BOTH carriers or it is a false credential: the value rides on
// the logView.channel_name column AND — on error rows, which build their own
// payload in recordRelayErrorLog — inside other. Blanking one while shipping
// the other hides nothing.
//
// channel_type is deliberately still visible: governance classifies it
// TierPublic and the vendor family is already implied by the model the caller
// chose. See TestInternalOtherKeys_NoPublicField.
func TestGetLogsV2_ChannelIdentityHiddenFromUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Note: Log.ChannelName is `gorm:"->"` — read-only, filled by a join, never
	// persisted. Seeding it here would assert nothing, so the column half of
	// this guarantee is locked directly against toLogViews in
	// TestToLogViews_BlanksChannelNameForUsersOnly below; this test covers the
	// half that does round-trip through the database, the `other` payload.
	row := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeError,
		ModelName: "gpt-4",
		CreatedAt: 1700000000,
		ChannelId: 7,
		Other:     `{"error_code":"upstream_5xx","channel_id":7,"channel_name":"openai-primary-key3","channel_type":1,"status_code":502}`,
	}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("failed to seed log: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs", nil, nil)
	resp := AssertV2Success(t, w)
	logs := resp["data"].(map[string]interface{})["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	lg := logs[0].(map[string]interface{})

	// The console falls back to "#<id>", so the row stays identifiable.
	if got, ok := lg["channel"].(float64); !ok || got != 7 {
		t.Errorf("channel id = %v, want 7 — blanking the name must not blank the opaque id "+
			"the console falls back to", lg["channel"])
	}

	other, ok := lg["other"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sanitized `other` object on the user route, got %T", lg["other"])
	}
	for _, f := range []string{"channel_name", "channel_id"} {
		if _, exists := other[f]; exists {
			t.Errorf("error-log `other` leaked %q to a non-admin — blanking the column while "+
				"shipping the payload hides nothing", f)
		}
	}
	// The caller must still be able to see why their own request failed.
	for _, f := range []string{"error_code", "status_code", "channel_type"} {
		if _, exists := other[f]; !exists {
			t.Errorf("user projection dropped %q — the caller needs to know why their own "+
				"request failed", f)
		}
	}

	// The tenant-admin route is the audit surface and keeps everything.
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/all", nil, nil)
	resp = AssertV2Success(t, w)
	logs = resp["data"].(map[string]interface{})["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("admin route: expected 1 log, got %d", len(logs))
	}
	adminOther := logs[0].(map[string]interface{})["other"].(map[string]interface{})
	if _, exists := adminOther["channel_name"]; !exists {
		t.Error("admin route must keep other.channel_name — it is the audit surface")
	}
}

// TestToLogViews_BlanksChannelNameForUsersOnly drives the projection directly
// because Log.ChannelName is `gorm:"->"`: it is populated by a join at read
// time and cannot be seeded through the ORM, so an end-to-end test would assert
// an empty string that was already empty for the wrong reason.
func TestToLogViews_BlanksChannelNameForUsersOnly(t *testing.T) {
	rows := []*repo.Log{{
		Id:          1,
		ChannelId:   7,
		ChannelName: "openai-primary-key3",
		ModelName:   "gpt-4",
	}}

	user := toLogViews(rows, false)
	if len(user) != 1 {
		t.Fatalf("expected 1 view, got %d", len(user))
	}
	if user[0].ChannelName != "" {
		t.Errorf("user view channel_name = %q, want empty — the operator's channel naming "+
			"describes our upstream accounts, not the caller's request (TierInternal in "+
			"governance/classification.go, and blanked by the v1 self-log route since forever)",
			user[0].ChannelName)
	}
	if user[0].ChannelId != 7 {
		t.Errorf("user view channel id = %d, want 7 — the console falls back to #<id>, so "+
			"blanking the name must not blank the id", user[0].ChannelId)
	}

	admin := toLogViews(rows, true)
	if admin[0].ChannelName != "openai-primary-key3" {
		t.Errorf("admin view channel_name = %q, want the real name — the tenant-admin route "+
			"is how an operator finds the failing upstream", admin[0].ChannelName)
	}
}
