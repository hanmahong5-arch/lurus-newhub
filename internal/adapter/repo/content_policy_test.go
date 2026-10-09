package repo

// content_policy_test.go - migration 050: log retention chokepoint, content
// rule store, override template store. Hermetic SQLite tier.

import (
	"errors"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

type retentionFixture struct {
	tenant *Tenant
	token  *Token
	userId int
}

func setupRetentionFixture(t *testing.T) (retentionFixture, func()) {
	t.Helper()
	cleanup := setupSQLiteDB(t)
	InvalidateContentRetentionCache()
	prevConsume, prevExport := common.LogConsumeEnabled, common.DataExportEnabled
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	ten := seedTenant(t, "ret-tenant", "ret", "Ret Tenant")
	u := seedUser(t, "ret-user", "ret@test.local", common.RoleCommonUser, common.UserStatusEnabled, ten.Id)
	tok := &Token{UserId: u.Id, TenantId: ten.Id, Key: common.GetRandomString(48), Name: "ret-tok", Status: common.TokenStatusEnabled}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatal(err)
	}
	return retentionFixture{tenant: ten, token: tok, userId: u.Id}, func() {
		common.LogConsumeEnabled, common.DataExportEnabled = prevConsume, prevExport
		contentpolicy.SetPlatformDefault(nil)
		InvalidateContentRetentionCache()
		cleanup()
	}
}

func writeConsume(t *testing.T, f retentionFixture, content string) *Log {
	t.Helper()
	c := newTenantScopeGinCtx("ret-user", f.tenant.Id)
	RecordConsumeLog(c, f.userId, RecordConsumeLogParams{
		ChannelId: 3, PromptTokens: 11, CompletionTokens: 22, ModelName: "model-a",
		TokenName: f.token.Name, TokenId: f.token.Id, Quota: 77, UseTimeSeconds: 4,
		ChannelType: 1, Content: content, RequestFingerprint: "fp-1",
		Other: map[string]interface{}{"frt": 120.0, "reject_reason": "echoed prompt text", "request_id": "rid-1"},
	})
	var lg Log
	if err := LOG_DB.Where("content = ? OR (token_id = ? AND type = ?)", content, f.token.Id, LogTypeConsume).
		Order("id desc").First(&lg).Error; err != nil {
		t.Fatalf("consume row missing: %v", err)
	}
	return &lg
}

func assertUsageIntact(t *testing.T, lg *Log) {
	t.Helper()
	if lg.PromptTokens != 11 || lg.CompletionTokens != 22 || lg.Quota != 77 || lg.UseTime != 4 ||
		lg.ModelName != "model-a" || lg.ChannelId != 3 {
		t.Fatalf("usage/cost/latency/model must survive every mode: %+v", lg)
	}
}

func TestRetentionFullKeepsEverything(t *testing.T) {
	f, done := setupRetentionFixture(t)
	defer done()
	lg := writeConsume(t, f, "secret prompt echo")
	if lg.Content != "secret prompt echo" || !strings.Contains(lg.Other, "reject_reason") || lg.RequestFingerprint != "fp-1" {
		t.Fatalf("full must not trim: %+v", lg)
	}
	assertUsageIntact(t, lg)
}

func TestRetentionTenantMetadataOnly(t *testing.T) {
	f, done := setupRetentionFixture(t)
	defer done()
	if err := SetTenantContentRetention(f.tenant.Id, contentpolicy.RetentionMetadataOnly); err != nil {
		t.Fatal(err)
	}
	lg := writeConsume(t, f, "secret prompt echo")
	if lg.Content != "" || strings.Contains(lg.Other, "reject_reason") || strings.Contains(lg.Other, "echoed") {
		t.Fatalf("content leaked: content=%q other=%s", lg.Content, lg.Other)
	}
	if !strings.Contains(lg.Other, "frt") || !strings.Contains(lg.Other, "rid-1") {
		t.Fatalf("metadata dropped: %s", lg.Other)
	}
	assertUsageIntact(t, lg)
}

func TestRetentionTokenNoneWithTenantFull(t *testing.T) {
	// Regression guard for the bifrost class of defect where a key-level
	// decision was overridden by a later layer: the token's stricter choice
	// must reach the stored row even though the tenant says full.
	f, done := setupRetentionFixture(t)
	defer done()
	if err := SetTenantContentRetention(f.tenant.Id, contentpolicy.RetentionFull); err != nil {
		t.Fatal(err)
	}
	if err := SetTokenContentRetention(f.token.Id, f.tenant.Id, contentpolicy.RetentionNone); err != nil {
		t.Fatal(err)
	}
	lg := writeConsume(t, f, "secret prompt echo")
	if lg.Content != "" || lg.RequestFingerprint != "" {
		t.Fatalf("token-level none not honoured: %+v", lg)
	}
	assertUsageIntact(t, lg)
}

func TestRetentionTokenCannotLoosenTenant(t *testing.T) {
	f, done := setupRetentionFixture(t)
	defer done()
	if err := SetTenantContentRetention(f.tenant.Id, contentpolicy.RetentionNone); err != nil {
		t.Fatal(err)
	}
	// Even if a row got "full" stored (direct SQL, a bug elsewhere), the
	// resolver still applies the tenant's stricter mode.
	if err := DB.Model(&Token{}).Where("id = ?", f.token.Id).Update("content_retention", "full").Error; err != nil {
		t.Fatal(err)
	}
	InvalidateContentRetentionCache()
	if got := EffectiveContentRetention(f.tenant.Id, f.token.Id); got != contentpolicy.RetentionNone {
		t.Fatalf("effective = %q, want none", got)
	}
	lg := writeConsume(t, f, "secret prompt echo")
	if lg.Content != "" {
		t.Fatalf("token loosened tenant: %q", lg.Content)
	}
}

func TestRetentionPlatformFloor(t *testing.T) {
	f, done := setupRetentionFixture(t)
	defer done()
	m := contentpolicy.RetentionMetadataOnly
	contentpolicy.SetPlatformDefault(&m)
	if err := SetTenantContentRetention(f.tenant.Id, contentpolicy.RetentionFull); err != nil {
		t.Fatal(err)
	}
	if got := EffectiveContentRetention(f.tenant.Id, f.token.Id); got != contentpolicy.RetentionMetadataOnly {
		t.Fatalf("tenant relaxed platform floor: %q", got)
	}
	if lg := writeConsume(t, f, "secret prompt echo"); lg.Content != "" {
		t.Fatalf("content kept under platform floor: %q", lg.Content)
	}
}

func TestRetentionAppliesToErrorLogs(t *testing.T) {
	f, done := setupRetentionFixture(t)
	defer done()
	if err := SetTokenContentRetention(f.token.Id, f.tenant.Id, contentpolicy.RetentionMetadataOnly); err != nil {
		t.Fatal(err)
	}
	c := newTenantScopeGinCtx("ret-user", f.tenant.Id)
	RecordErrorLog(c, f.userId, 3, "model-a", f.token.Name, "upstream echoed: the user's secret prompt",
		f.token.Id, 2, false, "default", map[string]interface{}{"error_code": "bad_request", "detail": "free text"})
	var lg Log
	if err := LOG_DB.Where("type = ? AND token_id = ?", LogTypeError, f.token.Id).First(&lg).Error; err != nil {
		t.Fatal(err)
	}
	if lg.Content != "" || strings.Contains(lg.Other, "free text") {
		t.Fatalf("error row leaked content: %q %s", lg.Content, lg.Other)
	}
	if !strings.Contains(lg.Other, "bad_request") || lg.ModelName != "model-a" || lg.ChannelId != 3 {
		t.Fatalf("error metadata lost: %+v", lg)
	}
}

func TestSetRetentionIsConfinedAndValidated(t *testing.T) {
	f, done := setupRetentionFixture(t)
	defer done()
	if err := SetTenantContentRetention(f.tenant.Id, "bogus"); err == nil {
		t.Error("invalid mode accepted")
	}
	if err := SetTokenContentRetention(f.token.Id, "another-tenant", contentpolicy.RetentionNone); err == nil {
		t.Error("token of another tenant was updated")
	}
	if err := SetTenantContentRetention("missing", contentpolicy.RetentionNone); err == nil {
		t.Error("missing tenant accepted")
	}
}

// ---- content rules ----------------------------------------------------

func setupRuleStore(t *testing.T) func() {
	t.Helper()
	cleanup := setupSQLiteDB(t)
	if err := DB.AutoMigrate(&ContentRule{}, &ChannelOverrideTemplate{}, &ChannelTemplateApplication{}); err != nil {
		t.Fatal(err)
	}
	InvalidateContentRulesCache()
	return func() { InvalidateContentRulesCache(); cleanup() }
}

func builtinRule(scope, tenant string, ordinal int64, kind, mode, builtin string) *ContentRule {
	return &ContentRule{Scope: scope, TenantId: tenant, Ordinal: ordinal, RoleScope: "any", Kind: kind,
		PatternType: "builtin", Builtin: builtin, Mode: mode, Enabled: true}
}

func TestContentRuleStoreValidatesAndLimits(t *testing.T) {
	defer setupRuleStore(t)()
	bad := builtinRule("tenant", "t1", 0, "mask", "enforce", "nope")
	if err := CreateContentRule(bad); !errors.Is(err, contentpolicy.ErrInvalidRule) {
		t.Fatalf("bad builtin accepted: %v", err)
	}
	for i := 0; i < contentpolicy.MaxRulesPerScope; i++ {
		if err := CreateContentRule(builtinRule("tenant", "t1", int64(i), "mask", "observe", "email")); err != nil {
			t.Fatalf("rule %d: %v", i, err)
		}
	}
	if err := CreateContentRule(builtinRule("tenant", "t1", 99, "mask", "observe", "email")); !errors.Is(err, ErrContentRuleLimit) {
		t.Fatalf("limit not enforced: %v", err)
	}
	// another tenant is unaffected
	if err := CreateContentRule(builtinRule("tenant", "t2", 0, "mask", "observe", "email")); err != nil {
		t.Fatalf("limit leaked across tenants: %v", err)
	}
}

func TestContentRuleTenantConfinement(t *testing.T) {
	defer setupRuleStore(t)()
	r := builtinRule("tenant", "t1", 0, "mask", "enforce", "phone_cn")
	if err := CreateContentRule(r); err != nil {
		t.Fatal(err)
	}
	if _, err := GetContentRule(r.Id, "tenant", "t2"); !errors.Is(err, ErrContentRuleNotFound) {
		t.Fatalf("t2 read t1's rule: %v", err)
	}
	if _, err := GetContentRule(r.Id, "platform", ""); !errors.Is(err, ErrContentRuleNotFound) {
		t.Fatalf("platform scope read a tenant rule: %v", err)
	}
	if err := DeleteContentRule(r.Id, "tenant", "t2"); !errors.Is(err, ErrContentRuleNotFound) {
		t.Fatalf("t2 deleted t1's rule: %v", err)
	}
	got, err := GetContentRule(r.Id, "tenant", "t1")
	if err != nil {
		t.Fatal(err)
	}
	got.Mode = "observe"
	got.TenantId = "t1"
	if err := SaveContentRule(got); err != nil {
		t.Fatal(err)
	}
	if err := DeleteContentRule(r.Id, "tenant", "t1"); err != nil {
		t.Fatal(err)
	}
}

func TestRulesetForTenantMergesPlatformAndTenant(t *testing.T) {
	defer setupRuleStore(t)()
	if err := CreateContentRule(builtinRule("platform", "", 5, "mask", "enforce", "email")); err != nil {
		t.Fatal(err)
	}
	if err := CreateContentRule(builtinRule("tenant", "t1", 1, "mask", "enforce", "phone_cn")); err != nil {
		t.Fatal(err)
	}
	if err := CreateContentRule(builtinRule("tenant", "t2", 1, "reject", "enforce", "bank_card")); err != nil {
		t.Fatal(err)
	}
	off := builtinRule("tenant", "t1", 2, "reject", "enforce", "id_card_cn")
	off.Enabled = false
	if err := CreateContentRule(off); err != nil {
		t.Fatal(err)
	}
	// Enabled=false is a zero value GORM would replace by the column default
	// on Create; force it so the disabled-rule path is really exercised.
	DB.Model(&ContentRule{}).Where("id = ?", off.Id).Update("enabled", false)
	InvalidateContentRulesCache()

	rs, err := ContentRulesetForTenant("t1")
	if err != nil || rs.Len() != 2 {
		t.Fatalf("t1 ruleset len=%d err=%v (want platform+own enabled only)", rs.Len(), err)
	}
	res := rs.Apply(contentpolicy.FormatOpenAIChat, []byte(`{"messages":[{"role":"user","content":"a@b.com 13800138000"}]}`))
	if !strings.Contains(string(res.Body), "[EMAIL]") || !strings.Contains(string(res.Body), "[PHONE]") {
		t.Fatalf("not masked: %s", res.Body)
	}
	if rs2, _ := ContentRulesetForTenant("nobody"); rs2.Len() != 1 {
		t.Fatalf("a tenant without rules still inherits the platform rule, got %d", rs2.Len())
	}
	// cache invalidation on write
	if err := CreateContentRule(builtinRule("tenant", "t1", 9, "mask", "enforce", "secret_key")); err != nil {
		t.Fatal(err)
	}
	if rs3, _ := ContentRulesetForTenant("t1"); rs3.Len() != 3 {
		t.Fatalf("write did not invalidate the ruleset cache: %d", rs3.Len())
	}
}

// ---- override templates ----------------------------------------------

func TestOverrideTemplateApplyRecordsSourceAndVersion(t *testing.T) {
	defer setupRuleStore(t)()
	ch1 := &Channel{Id: 901, Type: 1, Name: "c1", Key: "k", Status: 1}
	ch2 := &Channel{Id: 902, Type: 1, Name: "c2", Key: "k", Status: 1}
	if err := DB.Create([]*Channel{ch1, ch2}).Error; err != nil {
		t.Fatal(err)
	}
	tpl := &ChannelOverrideTemplate{Name: "tpl-a", ParamOverride: `{"temperature":0.2}`, HeaderOverride: `{"X-Test":"1"}`}
	if err := CreateChannelOverrideTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if err := CreateChannelOverrideTemplate(&ChannelOverrideTemplate{Name: "tpl-a"}); !errors.Is(err, ErrChannelTemplateExists) {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	res := ApplyChannelOverrideTemplate(tpl, []int{901, 902, 902, 9999}, 7)
	if len(res) != 3 || !res[0].Applied || !res[1].Applied || res[2].Applied {
		t.Fatalf("results: %+v", res)
	}
	var got Channel
	if err := DB.First(&got, "id = ?", 901).Error; err != nil {
		t.Fatal(err)
	}
	if got.ParamOverride == nil || *got.ParamOverride != `{"temperature":0.2}` || got.HeaderOverride == nil || *got.HeaderOverride != `{"X-Test":"1"}` {
		t.Fatalf("override not written: %+v", got)
	}
	app, err := LatestTemplateApplication(901)
	if err != nil || app == nil || app.TemplateId != tpl.Id || app.TemplateVersion != 1 || app.AppliedBy != 7 {
		t.Fatalf("application record: %+v %v", app, err)
	}
	// edit bumps the version; re-apply records the new one
	upd, err := UpdateChannelOverrideTemplate(tpl.Id, "tpl-a", "d", `{"temperature":0.5}`, "")
	if err != nil || upd.Version != 2 {
		t.Fatalf("update: %+v %v", upd, err)
	}
	ApplyChannelOverrideTemplate(upd, []int{901}, 7)
	app, _ = LatestTemplateApplication(901)
	if app.TemplateVersion != 2 {
		t.Fatalf("version not recorded: %+v", app)
	}
	// an empty header document leaves the channel's headers alone
	var again Channel
	DB.First(&again, "id = ?", 901)
	if again.HeaderOverride == nil || *again.HeaderOverride != `{"X-Test":"1"}` {
		t.Fatalf("empty template field clobbered the channel: %+v", again.HeaderOverride)
	}
	if err := DeleteChannelOverrideTemplate(tpl.Id); err != nil {
		t.Fatal(err)
	}
	if err := DeleteChannelOverrideTemplate(tpl.Id); !errors.Is(err, ErrChannelTemplateNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
