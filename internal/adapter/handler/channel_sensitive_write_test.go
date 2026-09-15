package handler

// channel_sensitive_write_test.go — oracles for L2 (cycle 9,
// auth-security-17/18 follow-up). Drives the REAL AddChannel/UpdateChannel
// (v1) and CreateChannelV2/UpdateChannelV2 (v2) handlers — v2 through the
// production router registration SetupV2TestRouter mounts (ListenAndServe
// via router.ServeHTTP, real requireTenantAdmin decision), v1 through
// v1Ctx (v1_cross_tenant_idor_test.go, same package) which sets exactly the
// context keys (role/tenant_id/id) middleware.AdminAuth's authHelper
// populates in production — the same convention every other v1 handler test
// in this package already uses (r2chanAdminCtx, TestV1Channel_*). In both
// cases the AUTHORIZATION DECISION under test — repo.HasActivePermissionGrant
// — runs for real against the test's own database; no test sets a "granted"
// outcome by hand.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// Predicate-level: channelWriteTouchesSensitiveField
// ============================================================================

// TestChannelSensitiveWrite_FieldSetIsExhaustive pins the exact sensitive
// field set the cycle-9 plan names (O5): key, base_url, param_override,
// header_override, and the per-channel proxy setting. Removing any one of
// these cases' "true" branch from channelWriteTouchesSensitiveField turns
// this red (mutation target). It also asserts the fields explicitly carved
// out (models, group, name, priority) and a non-proxy field living inside
// the SAME Setting JSON blob as proxy never trigger the gate.
func TestChannelSensitiveWrite_FieldSetIsExhaustive(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name     string
		req      *repo.Channel
		existing *repo.Channel
		want     bool
	}{
		{"key", &repo.Channel{Key: "sk-new"}, nil, true},
		{"base_url", &repo.Channel{BaseURL: strPtr("https://new.example.com")}, nil, true},
		{"param_override", &repo.Channel{ParamOverride: strPtr(`{"operations":[]}`)}, nil, true},
		{"header_override", &repo.Channel{HeaderOverride: strPtr(`{"X-Foo":"bar"}`)}, nil, true},
		{"proxy setting, no prior setting", &repo.Channel{Setting: strPtr(`{"proxy":"socks5://10.0.0.1:1080"}`)}, nil, true},
		{"proxy setting changed from existing", &repo.Channel{Setting: strPtr(`{"proxy":"socks5://10.0.0.2:1080"}`)}, &repo.Channel{Setting: strPtr(`{"proxy":"socks5://10.0.0.1:1080"}`)}, true},

		{"name is not sensitive", &repo.Channel{Name: "renamed"}, nil, false},
		{"models is not sensitive", &repo.Channel{Models: "gpt-4,gpt-5"}, nil, false},
		{"group is not sensitive", &repo.Channel{Group: "premium"}, nil, false},
		{"priority is not sensitive", &repo.Channel{Priority: func() *int64 { v := int64(5); return &v }()}, nil, false},
		{"weight is not sensitive", &repo.Channel{Weight: func() *uint { v := uint(5); return &v }()}, nil, false},
		{"status is not sensitive", &repo.Channel{Status: 2}, nil, false},
		{
			"non-proxy field inside the same Setting blob is not sensitive",
			&repo.Channel{Setting: strPtr(`{"force_format":true}`)},
			&repo.Channel{Setting: strPtr(`{"force_format":false}`)},
			false,
		},
		{"nil request touches nothing", nil, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := channelWriteTouchesSensitiveField(tc.existing, tc.req)
			if got != tc.want {
				t.Errorf("channelWriteTouchesSensitiveField(%+v, %+v) = %v, want %v", tc.existing, tc.req, got, tc.want)
			}
		})
	}
}

// ============================================================================
// v1: AddChannel / UpdateChannel
// ============================================================================

func seedSensitiveTestChannel(t *testing.T, ctx *V2TestContext, name string) *repo.Channel {
	t.Helper()
	return SeedV2Channel(t, ctx, name)
}

// TestUpdateChannel_V1_NonRootAdminWithoutGrant403 — mutation target: removing
// the enforceChannelSensitiveWrite call from UpdateChannel (v1) turns this
// red while the v2 twin (TestUpdateChannelV2_NonRootAdminWithoutGrant403)
// stays green, proving the two routes are independently gated.
func TestUpdateChannel_V1_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-update-no-grant")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":       ch.Id,
		"type":     ch.Type,
		"base_url": "https://8.8.8.8",
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["success"] == true {
		t.Errorf("expected success=false, body=%s", w.Body.String())
	}
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", body["error_code"])
	}
}

// TestUpdateChannel_V1_RefusedWriteLeavesRowUnchanged re-reads the row after
// the 403 and compares every column the predicate can touch — a silent
// partial write (the "key rotation that reports success and does nothing"
// failure mode the plan calls out) would leave this red even though the
// handler answered 403, if it had mutated the row before checking the gate.
func TestUpdateChannel_V1_RefusedWriteLeavesRowUnchanged(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-update-unchanged")
	originalKey := ch.Key
	originalBaseURL := ch.BaseURL
	originalParamOverride := ch.ParamOverride
	originalHeaderOverride := ch.HeaderOverride
	originalName := ch.Name

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":              ch.Id,
		"type":            ch.Type,
		"name":            "hijacked-name",
		"key":             "sk-attacker-controlled",
		"base_url":        "https://8.8.4.4",
		"param_override":  `{"operations":[]}`,
		"header_override": `{"X-Evil":"1"}`,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Key != originalKey {
		t.Errorf("Key mutated: got %q, want %q", reloaded.Key, originalKey)
	}
	gotBaseURL, wantBaseURL := "", ""
	if reloaded.BaseURL != nil {
		gotBaseURL = *reloaded.BaseURL
	}
	if originalBaseURL != nil {
		wantBaseURL = *originalBaseURL
	}
	if gotBaseURL != wantBaseURL {
		t.Errorf("BaseURL mutated: got %q, want %q", gotBaseURL, wantBaseURL)
	}
	if (reloaded.ParamOverride == nil) != (originalParamOverride == nil) {
		t.Errorf("ParamOverride mutated: got %v, want %v", reloaded.ParamOverride, originalParamOverride)
	}
	if (reloaded.HeaderOverride == nil) != (originalHeaderOverride == nil) {
		t.Errorf("HeaderOverride mutated: got %v, want %v", reloaded.HeaderOverride, originalHeaderOverride)
	}
	// Name was in the SAME request but is not a sensitive field; a naive
	// "reject the whole struct" gate would coincidentally also leave this
	// unchanged, but a naive "write everything then check" gate would not —
	// asserting it here catches that ordering bug too.
	if reloaded.Name != originalName {
		t.Errorf("Name mutated despite the refusal: got %q, want %q", reloaded.Name, originalName)
	}
}

// TestUpdateChannel_V1_RootAlwaysPasses.
func TestUpdateChannel_V1_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-update-root")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":       ch.Id,
		"type":     ch.Type,
		"base_url": "https://1.1.1.1",
	}, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["success"] != true {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.BaseURL == nil || *reloaded.BaseURL != "https://1.1.1.1" {
		t.Errorf("expected base_url persisted for root, got %v", reloaded.BaseURL)
	}
}

// TestUpdateChannel_V1_NonSensitiveFieldsUnaffected: ordinary channel
// administration (renaming) needs no grant.
func TestUpdateChannel_V1_NonSensitiveFieldsUnaffected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-update-name-only")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":   ch.Id,
		"type": ch.Type,
		"name": "renamed-by-plain-admin",
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["success"] != true {
		t.Fatalf("expected success=true for a non-sensitive update, body=%s", w.Body.String())
	}
}

// TestUpdateChannel_V1_WithGrantSucceeds: the round trip the UAT probe
// exercises — root grants channel:sensitive_write, and the SAME non-root
// admin whose bare request was refused above now succeeds and the row
// really changes.
func TestUpdateChannel_V1_WithGrantSucceeds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-update-with-grant")

	if _, err := repo.CreatePermissionGrant(ctx.AdminUser.Id, "channel", "sensitive_write", ctx.RootUser.Id); err != nil {
		t.Fatalf("seed channel:sensitive_write grant: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":       ch.Id,
		"type":     ch.Type,
		"base_url": "https://9.9.9.9",
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.BaseURL == nil || *reloaded.BaseURL != "https://9.9.9.9" {
		t.Errorf("expected base_url persisted once granted, got %v", reloaded.BaseURL)
	}
}

// TestCreateChannel_V1_NonRootAdminWithoutGrant403: creation carries the
// same power as an update — a create always populates Key.
func TestCreateChannel_V1_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := v1Ctx(http.MethodPost, "/api/channel/", AddChannelRequest{
		Mode:    "single",
		Channel: &repo.Channel{Type: 1, Key: "sk-created-by-admin", Name: "created-by-admin", Models: "gpt-4"},
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	AddChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", body["error_code"])
	}

	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "created-by-admin").Count(&count)
	if count != 0 {
		t.Errorf("expected no row created for a refused create, got %d", count)
	}
}

// ============================================================================
// v2: CreateChannelV2 / UpdateChannelV2 — through the real router
// (SetupV2TestRouter mounts the production handlers; V2RequestAsUser drives
// them via router.ServeHTTP).
// ============================================================================

func TestUpdateChannelV2_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v2-update-no-grant")

	body := map[string]interface{}{"base_url": "https://8.8.8.8"}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if resp["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", resp["error_code"])
	}

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	// SeedV2Channel's row carries the column's GORM default (a pointer to
	// "", not a nil pointer — gorm:"default:''") — "unset" for this column
	// means empty, not necessarily nil.
	if reloaded.BaseURL != nil && *reloaded.BaseURL != "" {
		t.Errorf("expected base_url to remain unset after a refused update, got %q", *reloaded.BaseURL)
	}
}

func TestUpdateChannelV2_WithGrantSucceeds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v2-update-with-grant")

	if _, err := repo.CreatePermissionGrant(ctx.AdminUser.Id, "channel", "sensitive_write", ctx.RootUser.Id); err != nil {
		t.Fatalf("seed channel:sensitive_write grant: %v", err)
	}

	body := map[string]interface{}{"base_url": "https://9.9.9.9"}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.BaseURL == nil || *reloaded.BaseURL != "https://9.9.9.9" {
		t.Errorf("expected base_url persisted once granted, got %v", reloaded.BaseURL)
	}
}

func TestUpdateChannelV2_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v2-update-root")

	body := map[string]interface{}{"base_url": "https://1.1.1.1"}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", ch.Id)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut, path, body, []string{"root"})

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)
}

// TestCreateChannelV2_NonRootAdminWithoutGrant403: v2 twin of
// TestCreateChannel_V1_NonRootAdminWithoutGrant403 — the two create routes
// are independently gated by the same mutation rule as the update pair.
func TestCreateChannelV2_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":   "v2-created-by-admin",
		"key":    "sk-v2-created-by-admin",
		"models": "gpt-4",
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if resp["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", resp["error_code"])
	}

	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "v2-created-by-admin").Count(&count)
	if count != 0 {
		t.Errorf("expected no row created for a refused v2 create, got %d", count)
	}
}

// ============================================================================
// Audit trail
// ============================================================================

// TestChannelSensitiveWriteRefused_RecordsAuditEvent proves every refusal is
// auditable (enterprise_acceptance) — the action landed in audit_action.go's
// const block AND its valid-actions set, and enforceChannelSensitiveWrite
// actually calls governance.RecordAuditEvent naming the actor and the
// channel.
func TestChannelSensitiveWriteRefused_RecordsAuditEvent(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("auto migrate audit tables: %v", err)
	}
	governance.SetAuditWriter(&pinnedAuditWriter{db: ctx.DB})

	ch := seedSensitiveTestChannel(t, ctx, "v1-update-audited")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":       ch.Id,
		"type":     ch.Type,
		"base_url": "https://8.8.8.8",
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	ev := pollAuditRow(t, governance.ActionChannelSensitiveWriteRefused, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionChannelSensitiveWriteRefused)
	}
	if ev.ActorID != ctx.AdminUser.Id {
		t.Errorf("ActorID = %d, want %d", ev.ActorID, ctx.AdminUser.Id)
	}
	if ev.Resource != governance.ResourceChannel {
		t.Errorf("Resource = %q, want %q", ev.Resource, governance.ResourceChannel)
	}
	if ev.ResourceID != ch.Id {
		t.Errorf("ResourceID = %d, want %d", ev.ResourceID, ch.Id)
	}
}
