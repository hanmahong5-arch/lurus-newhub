package handler

// channel_sensitive_write_test.go — oracles for L2 (cycle 9,
// auth-security-17/18 follow-up) plus the repair round that closed the
// eleven rulings (R1-R11) two adversarial acceptors' findings produced.
// Drives the REAL AddChannel/UpdateChannel/CopyChannel/ManageMultiKeys/
// EditTagChannels (v1) and CreateChannelV2/UpdateChannelV2 (v2) handlers
// directly at the predicate/handler-function level in this file; the
// REAL-CHAIN oracles that drive the actual production route table
// (SetApiRouter / SetApiV2Router, R5) live in
// internal/adapter/handler/router/channel_sensitive_write_real_chain_test.go
// — a package-handler test cannot mount that table itself (router imports
// handler, so the reverse import would cycle). v1 here goes through v1Ctx
// (v1_cross_tenant_idor_test.go, same package), which sets exactly the
// context keys (role/tenant_id/id) middleware.AdminAuth's authHelper
// populates in production — the same convention every other v1 handler
// test in this package already uses (r2chanAdminCtx, TestV1Channel_*); v2
// here goes through a test router that mirrors the v2 channel route table
// (production mounts the same handlers under AdminAuth + TenantSlugGuard,
// router/api-v2-router.go:170-181) via SetupV2TestRouter and
// router.ServeHTTP (A-4 repair-round wording fix). In both cases the
// AUTHORIZATION DECISION under test — repo.HasActivePermissionGrant — runs
// for real against the test's own database; no test sets a "granted"
// outcome by hand.

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// Predicate-level: channelWriteTouchesSensitiveField
// ============================================================================

// TestChannelSensitiveWrite_ClassifiesTheNamedFields (R8, repair-round
// ruling — renamed from the misleadingly-named
// TestChannelSensitiveWrite_FieldSetIsExhaustive, which claimed
// exhaustiveness a fixed hand-written table cannot prove; see
// TestChannelSensitiveWrite_FieldSetIsExhaustive below for the actual
// exhaustiveness guard) pins the exact sensitive field set: key, base_url,
// param_override, header_override, the per-channel proxy setting, type,
// other, and openai_organization (O5, widened by R2). Removing any one of
// these cases' "true" branch from channelWriteTouchesSensitiveField turns
// this red (mutation target). It also covers R1's value-diff fix directly
// at the predicate level (an UNCHANGED base_url must not trip the gate,
// mirroring what both shipped editors always resend) and asserts the
// fields explicitly carved out (models, group, name, priority) and a
// non-proxy field living inside the SAME Setting JSON blob as proxy never
// trigger the gate.
func TestChannelSensitiveWrite_ClassifiesTheNamedFields(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name     string
		req      *repo.Channel
		existing *repo.Channel
		want     bool
	}{
		{"key", &repo.Channel{Key: "sk-new"}, nil, true},
		{"base_url on create", &repo.Channel{BaseURL: strPtr("https://new.example.com")}, nil, true},
		{"param_override", &repo.Channel{ParamOverride: strPtr(`{"operations":[]}`)}, nil, true},
		{"header_override", &repo.Channel{HeaderOverride: strPtr(`{"X-Foo":"bar"}`)}, nil, true},
		{"proxy setting, no prior setting", &repo.Channel{Setting: strPtr(`{"proxy":"socks5://10.0.0.1:1080"}`)}, nil, true},
		{"proxy setting changed from existing", &repo.Channel{Setting: strPtr(`{"proxy":"socks5://10.0.0.2:1080"}`)}, &repo.Channel{Setting: strPtr(`{"proxy":"socks5://10.0.0.1:1080"}`)}, true},

		// R1: base_url/param_override/header_override are VALUE-diffed —
		// resending the exact stored value (what both shipped editors do
		// on every save, including a plain rename) must NOT trip the gate.
		{"base_url resent unchanged (both non-nil, equal)", &repo.Channel{BaseURL: strPtr("https://old.example.com")}, &repo.Channel{BaseURL: strPtr("https://old.example.com")}, false},
		{"base_url resent unchanged (nil existing, empty req — nil/empty equal)", &repo.Channel{BaseURL: strPtr("")}, &repo.Channel{BaseURL: nil}, false},
		{"base_url resent unchanged (empty existing, nil req)", &repo.Channel{BaseURL: nil}, &repo.Channel{BaseURL: strPtr("")}, false},
		{"base_url actually changed", &repo.Channel{BaseURL: strPtr("https://new.example.com")}, &repo.Channel{BaseURL: strPtr("https://old.example.com")}, true},
		{"param_override resent unchanged", &repo.Channel{ParamOverride: strPtr(`{"operations":[]}`)}, &repo.Channel{ParamOverride: strPtr(`{"operations":[]}`)}, false},
		{"param_override actually changed", &repo.Channel{ParamOverride: strPtr(`{"operations":[1]}`)}, &repo.Channel{ParamOverride: strPtr(`{"operations":[]}`)}, true},
		{"header_override resent unchanged", &repo.Channel{HeaderOverride: strPtr(`{"X-Foo":"bar"}`)}, &repo.Channel{HeaderOverride: strPtr(`{"X-Foo":"bar"}`)}, false},
		{"header_override actually changed", &repo.Channel{HeaderOverride: strPtr(`{"X-Foo":"baz"}`)}, &repo.Channel{HeaderOverride: strPtr(`{"X-Foo":"bar"}`)}, true},

		// R2: type/other/openai_organization join the set, also value-diffed
		// — the legacy console (EditChannelModal.jsx) resends all three on
		// every save.
		{"type resent unchanged", &repo.Channel{Type: 1}, &repo.Channel{Type: 1}, false},
		{"type actually changed", &repo.Channel{Type: 3}, &repo.Channel{Type: 1}, true},
		{"type on create", &repo.Channel{Type: 1}, nil, true},
		{"other resent unchanged", &repo.Channel{Other: "region=us"}, &repo.Channel{Other: "region=us"}, false},
		{"other actually changed", &repo.Channel{Other: "region=eu"}, &repo.Channel{Other: "region=us"}, true},
		{"other unset on request never touches an existing value (GORM skips zero string on Updates)", &repo.Channel{Other: ""}, &repo.Channel{Other: "region=us"}, false},
		{"openai_organization resent unchanged", &repo.Channel{OpenAIOrganization: strPtr("org-abc")}, &repo.Channel{OpenAIOrganization: strPtr("org-abc")}, false},
		{"openai_organization actually changed", &repo.Channel{OpenAIOrganization: strPtr("org-xyz")}, &repo.Channel{OpenAIOrganization: strPtr("org-abc")}, true},

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
		{
			"OtherSettings (json:settings, azure api-version etc.) is not sensitive — explicit non-goal (R2)",
			&repo.Channel{OtherSettings: `{"api_version":"2024-01-01"}`},
			&repo.Channel{OtherSettings: `{"api_version":"2023-01-01"}`},
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

// TestChannelSensitiveWrite_FieldSetIsExhaustive is the R8 reflection guard
// (repair-round ruling — this is a NEW test, distinct from the renamed
// table test above): every json-tagged field on repo.Channel must be
// classified into EXACTLY ONE of sensitiveJSONFields /
// nonSensitiveJSONFields below, mirroring
// new-api-upstream/controller/channel_authz.go:36-55's exhaustiveness
// check. A field added to repo.Channel later that lands in neither set
// fails this test loudly instead of silently defaulting to "not
// sensitive" the way the four fields in finding B-3 did.
func TestChannelSensitiveWrite_FieldSetIsExhaustive(t *testing.T) {
	sensitiveJSONFields := map[string]bool{
		"key": true, "base_url": true, "param_override": true,
		"header_override": true, "setting": true, "type": true,
		"other": true, "openai_organization": true,
	}
	nonSensitiveJSONFields := map[string]bool{
		"id": true, "tenant_id": true, "test_model": true, "status": true,
		"name": true, "weight": true, "created_time": true, "test_time": true,
		"response_time": true, "balance": true, "balance_updated_time": true,
		"models": true, "group": true, "used_quota": true, "model_mapping": true,
		"status_code_mapping": true, "priority": true, "auto_ban": true,
		"other_info": true, "tag": true, "remark": true, "channel_info": true,
		// "settings" = OtherSettings (azure api-version etc.) — explicit
		// non-goal named in this file's header and in the table test above.
		"settings":               true,
		"managed_models_by_sync": true, "last_sync_fetch_count": true,
	}

	typ := reflect.TypeOf(repo.Channel{})
	seen := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		jsonTag := f.Tag.Get("json")
		if jsonTag == "" {
			t.Fatalf("repo.Channel field %s has no json tag — cannot classify it", f.Name)
		}
		name := strings.Split(jsonTag, ",")[0]
		if name == "-" {
			continue // e.g. Keys — never serialized, never bound from a request
		}
		seen++
		inSensitive, inNonSensitive := sensitiveJSONFields[name], nonSensitiveJSONFields[name]
		if !inSensitive && !inNonSensitive {
			t.Errorf("repo.Channel field %s (json:%q) is classified in NEITHER the sensitive nor the explicit non-sensitive set — update channel_sensitive_write.go's predicate AND one of the two sets in this test", f.Name, name)
		}
		if inSensitive && inNonSensitive {
			t.Errorf("repo.Channel field %s (json:%q) is classified in BOTH sets", f.Name, name)
		}
	}
	if seen == 0 {
		t.Fatalf("reflect.TypeOf(repo.Channel{}) reported zero json-tagged fields — the guard did not actually run")
	}

	// The classification above is a static map: on its own, deleting a field
	// from channelWriteTouchesSensitiveField leaves it green. So every name in
	// sensitiveJSONFields must also DRIVE the predicate — a request that
	// populates only that field has to read as a sensitive write. Dropping any
	// one branch from the predicate turns this red and names the field.
	populate := map[string]func(*repo.Channel){
		"key":                 func(ch *repo.Channel) { ch.Key = "sk-exhaustiveness-probe" },
		"base_url":            func(ch *repo.Channel) { ch.BaseURL = common.GetPointer[string]("https://probe.invalid") },
		"param_override":      func(ch *repo.Channel) { ch.ParamOverride = common.GetPointer[string](`{"operations":[]}`) },
		"header_override":     func(ch *repo.Channel) { ch.HeaderOverride = common.GetPointer[string](`{"X-Probe":"1"}`) },
		"openai_organization": func(ch *repo.Channel) { ch.OpenAIOrganization = common.GetPointer[string]("org-probe") },
		"type":                func(ch *repo.Channel) { ch.Type = 42 },
		"other":               func(ch *repo.Channel) { ch.Other = "probe" },
		"setting": func(ch *repo.Channel) {
			ch.Setting = common.GetPointer[string](`{"proxy":"http://probe.invalid:8080"}`)
		},
	}
	for name := range sensitiveJSONFields {
		drive, ok := populate[name]
		if !ok {
			t.Fatalf("sensitiveJSONFields lists %q but this guard has no way to populate it — add one, or the field is classified sensitive with nothing proving the predicate reads it", name)
		}
		req := &repo.Channel{}
		drive(req)
		if !channelWriteTouchesSensitiveField(nil, req) {
			t.Errorf("a request populating only %q reads as NOT sensitive — channelWriteTouchesSensitiveField no longer looks at that field", name)
		}
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

// TestUpdateChannel_V1_LegacyConsoleShapedRename_Success is R1's oracle for
// v1: the legacy channel editor (EditChannelModal.jsx:1310, spreading
// localInputs whose originInputs.key/base_url/other default to ” at
// :130-141 and whose type/openai_organization are pre-filled from the
// channel being edited) always resends type/base_url/other/
// openai_organization on EVERY save, including a plain rename — key is the
// one field it truly never echoes back (stays ""). Before R1 this exact
// body 403'd every legacy-console edit for a non-root admin; it must now
// succeed and persist only the field that actually changed (name).
func TestUpdateChannel_V1_LegacyConsoleShapedRename_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-legacy-console-rename")
	// Give the seeded channel a non-zero Other/OpenAIOrganization/BaseURL so
	// the "resend the same value" path is actually exercised, not just the
	// zero-value default.
	openaiOrg := "org-existing"
	baseURL := "https://8.8.4.4"
	ch.Other = "region=us"
	ch.OpenAIOrganization = &openaiOrg
	ch.BaseURL = &baseURL
	if err := repo.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed pre-existing sensitive fields: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":                  ch.Id,
		"type":                ch.Type, // resent, unchanged
		"name":                "renamed-by-plain-admin",
		"key":                 "",          // legacy editor never echoes the stored key
		"base_url":            baseURL,     // resent, unchanged
		"other":               "region=us", // resent, unchanged
		"openai_organization": openaiOrg,   // resent, unchanged
		"models":              ch.Models,
		"group":               ch.Group,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["success"] != true {
		t.Fatalf("expected success=true for a legacy-console-shaped rename, body=%s", w.Body.String())
	}
	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Name != "renamed-by-plain-admin" {
		t.Errorf("Name = %q, want the rename to persist", reloaded.Name)
	}
}

// TestUpdateChannel_V1_LegacyConsoleShapedBody_DifferentBaseURL403 is R1's
// negative twin: the SAME legacy-console-shaped body, but base_url actually
// differs from the stored value — still 403, and the row (including the
// name field carried in the same request) is unchanged.
func TestUpdateChannel_V1_LegacyConsoleShapedBody_DifferentBaseURL403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-legacy-console-diff-baseurl")
	originalName := ch.Name

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":       ch.Id,
		"type":     ch.Type,
		"name":     "attempted-rename-alongside-key-swap",
		"key":      "",
		"base_url": "https://8.8.8.8",
		"models":   ch.Models,
		"group":    ch.Group,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Name != originalName {
		t.Errorf("Name mutated despite the refusal: got %q, want %q", reloaded.Name, originalName)
	}
	if reloaded.BaseURL != nil && *reloaded.BaseURL != "" {
		t.Errorf("BaseURL mutated despite the refusal: got %q", *reloaded.BaseURL)
	}
}

// derefOrEmpty reads a *string column as "" when unset, so a nil/"" pair
// does not read as a mutation.
func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TestUpdateChannel_V1_RefusedWriteLeavesRowUnchanged re-reads the row after
// the 403 and compares each of the eight columns named in
// channel_sensitive_write.go's header, plus name — a silent
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
	originalType := ch.Type
	originalOther := ch.Other
	originalOrganization := ch.OpenAIOrganization
	originalSetting := ch.Setting

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":                  ch.Id,
		"type":                ch.Type + 1,
		"name":                "hijacked-name",
		"key":                 "sk-attacker-controlled",
		"base_url":            "https://8.8.4.4",
		"param_override":      `{"operations":[]}`,
		"header_override":     `{"X-Evil":"1"}`,
		"other":               "hijacked-other",
		"openai_organization": "org-hijacked",
		"setting":             `{"proxy":"http://203.0.113.9:8080"}`, // literal public IP: no DNS lookup in the egress guard
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
	if reloaded.Type != originalType {
		t.Errorf("Type mutated: got %d, want %d", reloaded.Type, originalType)
	}
	if reloaded.Other != originalOther {
		t.Errorf("Other mutated: got %q, want %q", reloaded.Other, originalOther)
	}
	if derefOrEmpty(reloaded.OpenAIOrganization) != derefOrEmpty(originalOrganization) {
		t.Errorf("OpenAIOrganization mutated: got %q, want %q",
			derefOrEmpty(reloaded.OpenAIOrganization), derefOrEmpty(originalOrganization))
	}
	if derefOrEmpty(reloaded.Setting) != derefOrEmpty(originalSetting) {
		t.Errorf("Setting mutated: got %q, want %q",
			derefOrEmpty(reloaded.Setting), derefOrEmpty(originalSetting))
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

	if _, _, err := repo.CreatePermissionGrant(ctx.AdminUser.Id, "channel", "sensitive_write", ctx.RootUser.Id); err != nil {
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

// TestCreateChannel_V1_RootAlwaysPasses: root's create is not gated —
// completes the "root is never refused" enterprise_acceptance criterion for
// the create route specifically (the update/copy/tag variants each have
// their own RootAlwaysPasses test already).
func TestCreateChannel_V1_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := v1Ctx(http.MethodPost, "/api/channel/", AddChannelRequest{
		Mode:    "single",
		Channel: &repo.Channel{Type: 1, Key: "sk-created-by-root", Name: "created-by-root", Models: "gpt-4"},
	}, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	AddChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "created-by-root").Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row created for root's create, got %d", count)
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

// TestUpdateChannelV2_ConsoleShapedRename_Success is the oracle for v2:
// the exact body web/src/pages/v2/Channel/index.jsx:445-456 builds
// (name/type/base_url/models/group/weight/priority/model_mapping/tag/
// remark — base_url is ALWAYS included, pre-filled from source.base_url at
// :417), resending the stored base_url unchanged. The stored value here is
// deliberately NON-empty: with both sides empty this would also pass under
// a rule that merely ignores empty strings, so it could not tell a value
// diff from a presence check. A non-root admin without a grant must still
// be able to rename through this exact console request shape.
func TestUpdateChannelV2_ConsoleShapedRename_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v2-console-shaped-rename")
	const storedBaseURL = "https://203.0.113.10/v1" // literal public IP: no DNS lookup, so the SSRF guard stays hermetic
	ch.BaseURL = common.GetPointer[string](storedBaseURL)
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed a non-empty base_url: %v", err)
	}

	body := map[string]interface{}{
		"name":          "renamed-via-console",
		"type":          float64(ch.Type),
		"base_url":      storedBaseURL, // the console resends the stored value verbatim
		"models":        ch.Models,
		"group":         ch.Group,
		"weight":        float64(1),
		"priority":      float64(0),
		"model_mapping": "",
		"tag":           "",
		"remark":        "",
	}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Name != "renamed-via-console" {
		t.Errorf("Name = %q, want the rename to persist", reloaded.Name)
	}
	if reloaded.BaseURL == nil || *reloaded.BaseURL != storedBaseURL {
		t.Errorf("BaseURL = %v, want the resent value %q to survive the rename", reloaded.BaseURL, storedBaseURL)
	}
}

// TestUpdateChannelV2_ConsoleShapedBody_DifferentBaseURL403 is R1's
// negative twin: the SAME console-shaped body, but base_url actually
// differs from the stored value — still 403, and the row (including the
// name carried in the same request) is unchanged.
func TestUpdateChannelV2_ConsoleShapedBody_DifferentBaseURL403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v2-console-shaped-diff-baseurl")
	originalName := ch.Name

	body := map[string]interface{}{
		"name":          "attempted-rename-alongside-key-swap",
		"type":          float64(ch.Type),
		"base_url":      "https://8.8.4.4",
		"models":        ch.Models,
		"group":         ch.Group,
		"weight":        float64(1),
		"priority":      float64(0),
		"model_mapping": "",
		"tag":           "",
		"remark":        "",
	}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Name != originalName {
		t.Errorf("Name mutated despite the refusal: got %q, want %q", reloaded.Name, originalName)
	}
}

func TestUpdateChannelV2_WithGrantSucceeds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v2-update-with-grant")

	if _, _, err := repo.CreatePermissionGrant(ctx.AdminUser.Id, "channel", "sensitive_write", ctx.RootUser.Id); err != nil {
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

// TestCreateChannelV2_RootAlwaysPasses: v2 twin of
// TestCreateChannel_V1_RootAlwaysPasses.
func TestCreateChannelV2_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":   "v2-created-by-root",
		"key":    "sk-v2-created-by-root",
		"models": "gpt-4",
	}
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"root"})

	AssertV2Status(t, w, http.StatusCreated)
	AssertV2Success(t, w)

	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "v2-created-by-root").Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row created for root's v2 create, got %d", count)
	}
}

// ============================================================================
// Fail-closed on a grant lookup error (R6, repair-round ruling / A-3)
// ============================================================================

// TestUpdateChannel_V1_GrantLookupErrorDenies is A-3's oracle: forcing
// repo.HasActivePermissionGrant to fail (by dropping the table it queries)
// must still deny the write — 403, no mutation — proving the `err == nil &&
// granted` guard in enforceChannelSensitiveWrite actually matters. Before
// this test, rewriting that guard to `granted, _ :=
// repo.HasActivePermissionGrant(...)` / `if granted {` left the whole
// package green (measured by the acceptor at ok 94.686s) because nothing
// ever drove the lookup into an error state.
func TestUpdateChannel_V1_GrantLookupErrorDenies(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-grant-lookup-error")
	originalName := ch.Name

	if err := ctx.DB.Migrator().DropTable(&entity.AdminPermissionGrant{}); err != nil {
		t.Fatalf("drop admin_permission_grants table: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":       ch.Id,
		"type":     ch.Type,
		"base_url": "https://8.8.8.8",
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fail closed on a grant lookup error); body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", body["error_code"])
	}

	// The table is gone, so GetChannelById itself would error if the
	// handler had reached any write path — reconstruct the channel table's
	// row directly via a raw scan to prove no mutation slipped through
	// before the gate ran.
	var name string
	if err := ctx.DB.Table("channels").Select("name").Where("id = ?", ch.Id).Scan(&name).Error; err != nil {
		t.Fatalf("read channel name via raw scan: %v", err)
	}
	if name != originalName {
		t.Errorf("Name mutated despite the lookup-error refusal: got %q, want %q", name, originalName)
	}
}

// ============================================================================
// CopyChannel / ManageMultiKeys (R3, repair-round ruling / B-4)
// ============================================================================

// TestCopyChannel_NonRootAdminWithoutGrant403: cloning a channel duplicates
// its stored key/base_url onto a brand-new row — the same power as a
// create. Mutation target: removing the enforceChannelSensitiveWrite call
// from CopyChannel turns this red.
func TestCopyChannel_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-copy-source")
	var beforeCount int64
	ctx.DB.Model(&repo.Channel{}).Count(&beforeCount)

	c, w := v1Ctx(http.MethodPost, fmt.Sprintf("/api/channel/copy/%d", ch.Id), nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ch.Id)}}
	CopyChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", body["error_code"])
	}

	var afterCount int64
	ctx.DB.Model(&repo.Channel{}).Count(&afterCount)
	if afterCount != beforeCount {
		t.Errorf("expected no clone row created for a refused copy, got %d new row(s)", afterCount-beforeCount)
	}
}

// TestCopyChannel_RootAlwaysPasses: root's copy still succeeds and the
// clone carries the source's key.
func TestCopyChannel_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-copy-source-root")

	c, w := v1Ctx(http.MethodPost, fmt.Sprintf("/api/channel/copy/%d", ch.Id), nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ch.Id)}}
	CopyChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["success"] != true {
		t.Fatalf("expected success=true for root's copy, body=%s", w.Body.String())
	}
}

// TestManageMultiKeys_DeleteKey_NonRootAdminWithoutGrant403: mutation
// target for the delete_key branch — removing its
// enforceChannelSensitiveWrite call turns this red.
func TestManageMultiKeys_DeleteKey_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-multikey-delete")
	ch.Key = "sk-one\nsk-two"
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed multi-key channel: %v", err)
	}
	originalKey := ch.Key

	keyIndex := 0
	c, w := v1Ctx(http.MethodPost, "/api/channel/multi_key/manage", MultiKeyManageRequest{
		ChannelId: ch.Id, Action: "delete_key", KeyIndex: &keyIndex,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Key != originalKey {
		t.Errorf("Key mutated despite the refusal: got %q, want %q", reloaded.Key, originalKey)
	}
}

// TestManageMultiKeys_DeleteDisabledKeys_NonRootAdminWithoutGrant403:
// mutation target for the delete_disabled_keys branch.
func TestManageMultiKeys_DeleteDisabledKeys_NonRootAdminWithoutGrant403(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-multikey-delete-disabled")
	ch.Key = "sk-one\nsk-two"
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: 3} // auto-disabled
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed multi-key channel: %v", err)
	}
	originalKey := ch.Key

	c, w := v1Ctx(http.MethodPost, "/api/channel/multi_key/manage", MultiKeyManageRequest{
		ChannelId: ch.Id, Action: "delete_disabled_keys",
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Key != originalKey {
		t.Errorf("Key mutated despite the refusal: got %q, want %q", reloaded.Key, originalKey)
	}
}

// TestManageMultiKeys_EnableKey_NonSensitive_Unaffected proves the gate is
// scoped to delete_key/delete_disabled_keys only — enable_key (which
// never touches the Key column, only the status map) needs no grant.
func TestManageMultiKeys_EnableKey_NonSensitive_Unaffected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-multikey-enable")
	ch.Key = "sk-one\nsk-two"
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: 2} // manually disabled
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed multi-key channel: %v", err)
	}

	keyIndex := 1
	c, w := v1Ctx(http.MethodPost, "/api/channel/multi_key/manage", MultiKeyManageRequest{
		ChannelId: ch.Id, Action: "enable_key", KeyIndex: &keyIndex,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (enable_key is not gated); body=%s", w.Code, w.Body.String())
	}
}

// TestManageMultiKeys_DeleteKey_RootAlwaysPasses completes the "root is
// never refused" criterion for the two gated ManageMultiKeys branches.
func TestManageMultiKeys_DeleteKey_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-multikey-delete-root")
	ch.Key = "sk-one\nsk-two"
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed multi-key channel: %v", err)
	}

	keyIndex := 0
	c, w := v1Ctx(http.MethodPost, "/api/channel/multi_key/manage", MultiKeyManageRequest{
		ChannelId: ch.Id, Action: "delete_key", KeyIndex: &keyIndex,
	}, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	ManageMultiKeys(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["success"] != true {
		t.Fatalf("expected success=true for root's delete_key, body=%s", w.Body.String())
	}
}

// ============================================================================
// EditTagChannels (R4, repair-round ruling — withdraws the earlier
// "tag-scoped edits are out of scope" non-goal)
// ============================================================================

// TestEditTagChannels_NonRootAdminWithoutGrant403_ParamOverride: mutation
// target — removing the enforceChannelSensitiveWrite call from
// EditTagChannels turns this red.
func TestEditTagChannels_NonRootAdminWithoutGrant403_ParamOverride(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-tag-edit-source")
	tag := "shared-tag-po"
	ch.Tag = &tag
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed tagged channel: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/tag", ChannelTag{
		Tag:           tag,
		ParamOverride: common.GetPointer[string](`{"operations":[]}`),
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EditTagChannels(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", body["error_code"])
	}

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.ParamOverride != nil {
		t.Errorf("ParamOverride mutated despite the refusal: got %v", reloaded.ParamOverride)
	}
}

// TestEditTagChannels_NonRootAdminWithoutGrant403_HeaderOverride is
// header_override's twin — the two fields are independently checked in the
// synthetic req the gate builds from ChannelTag.
func TestEditTagChannels_NonRootAdminWithoutGrant403_HeaderOverride(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-tag-edit-source-ho")
	tag := "shared-tag-ho"
	ch.Tag = &tag
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed tagged channel: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/tag", ChannelTag{
		Tag:            tag,
		HeaderOverride: common.GetPointer[string](`{"X-Foo":"bar"}`),
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EditTagChannels(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

// TestEditTagChannels_NonSensitiveFieldsUnaffected: editing only
// non-sensitive fields on the tag (priority/weight/models) needs no grant.
func TestEditTagChannels_NonSensitiveFieldsUnaffected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-tag-edit-nonsensitive")
	tag := "shared-tag-plain"
	ch.Tag = &tag
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed tagged channel: %v", err)
	}

	priority := int64(7)
	c, w := v1Ctx(http.MethodPut, "/api/channel/tag", ChannelTag{
		Tag:      tag,
		Priority: &priority,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EditTagChannels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (priority/weight/models are not gated); body=%s", w.Code, w.Body.String())
	}
}

// TestEditTagChannels_RootAlwaysPasses: root editing param_override on a
// tag still succeeds.
func TestEditTagChannels_RootAlwaysPasses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-tag-edit-root")
	tag := "shared-tag-root"
	ch.Tag = &tag
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed tagged channel: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/tag", ChannelTag{
		Tag:           tag,
		ParamOverride: common.GetPointer[string](`{"operations":[]}`),
	}, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	EditTagChannels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
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

// TestUpdateChannel_V1_AuthorizationPrecedesValidation pins the ORDER of the
// two refusals in v1 UpdateChannel. The request below is both unauthorized
// (ungranted non-root admin touching base_url) and invalid (param_override
// carries an operation mode the override engine does not implement). It must
// answer 403 PERMISSION_DENIED and write the refusal audit row. Validating
// first returned the document error and never reached the gate, which told an
// ungranted caller whether their payload would have validated and left the
// denied attempt unaudited.
func TestUpdateChannel_V1_AuthorizationPrecedesValidation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("auto migrate audit tables: %v", err)
	}
	governance.SetAuditWriter(&pinnedAuditWriter{db: ctx.DB})

	ch := seedSensitiveTestChannel(t, ctx, "v1-authz-before-validation")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", map[string]interface{}{
		"id":             ch.Id,
		"type":           ch.Type,
		"base_url":       "https://8.8.8.8",
		"param_override": `{"operations":[{"path":"model","mode":"not_a_real_mode","value":"x"}]}`,
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (authorization must be decided before the document is validated); body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Fatalf("error_code = %v, want PERMISSION_DENIED; body=%s", body["error_code"], w.Body.String())
	}
	if ev := pollAuditRow(t, governance.ActionChannelSensitiveWriteRefused, 2*time.Second); ev == nil {
		t.Fatalf("no %s audit row found within timeout — the denied attempt was not recorded", governance.ActionChannelSensitiveWriteRefused)
	}
}

// TestEditTagChannels_NonRootAdminWithoutGrant403_ClearsParamOverride is the
// case the value-diff rule got wrong. A body carrying param_override:"" CLEARS
// a real override on every channel under the tag, which is as powerful as
// setting one, but nil and "" compare equal under strPtrChanged — so routing
// the tag editor through channelWriteTouchesSensitiveField let an ungranted
// non-root admin wipe an operations document with no 403 and no audit row.
// The gate is on PRESENCE here; reverting EditTagChannels to the diff rule
// turns this red while the two tests above stay green.
func TestEditTagChannels_NonRootAdminWithoutGrant403_ClearsParamOverride(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedSensitiveTestChannel(t, ctx, "v1-tag-edit-clear-po")
	tag := "shared-tag-clear-po"
	ch.Tag = &tag
	const liveOverride = `{"operations":[{"path":"$.temperature","mode":"set","value":0.1}]}`
	ch.ParamOverride = common.GetPointer[string](liveOverride)
	if err := ctx.DB.Save(ch).Error; err != nil {
		t.Fatalf("seed tagged channel with a live param_override: %v", err)
	}

	c, w := v1Ctx(http.MethodPut, "/api/channel/tag", ChannelTag{
		Tag:           tag,
		ParamOverride: common.GetPointer[string](""),
	}, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EditTagChannels(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if body["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", body["error_code"])
	}

	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.ParamOverride == nil || *reloaded.ParamOverride != liveOverride {
		t.Errorf("ParamOverride = %v, want the seeded document untouched", reloaded.ParamOverride)
	}
}
