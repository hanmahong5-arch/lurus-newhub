package handler

// v2_data_policy_test.go - migration 050 relay data control: tenant-admin
// retention + content rules, channel override templates, and the relay-entry
// content-rule application (all four formats, pass-through, observe, reject).
//
// Test names referenced by router/v2_completeness_test.go's swept map:
//   TestContentRuleV2_CrossTenantIs404
//   TestTokenRetentionV2_CrossTenantIs404

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const dpPhone = "13800138000"

type dpFixture struct {
	*V2TestContext
	engine *gin.Engine
}

func setupDataPolicy(t *testing.T) *dpFixture {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&repo.ContentRule{}, &repo.ChannelOverrideTemplate{}, &repo.ChannelTemplateApplication{}); err != nil {
		t.Fatal(err)
	}
	repo.InvalidateContentRulesCache()
	repo.InvalidateContentRetentionCache()
	prevMaxBody := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 32
	contentpolicy.SetPlatformDefault(nil)

	auth := func(c *gin.Context) {
		tenantID := c.GetHeader("X-Test-Tenant-ID")
		if tenantID == "" {
			tenantID = ctx.TenantID
		}
		uid, _ := strconv.Atoi(c.GetHeader("X-Test-User-ID"))
		var roles []string
		if h := c.GetHeader("X-Test-Roles"); h != "" {
			roles = strings.Split(h, ",")
		}
		c.Set("tenant_context", &middleware.TenantContext{TenantID: tenantID, UserID: uid, Roles: roles})
		c.Set("tenant_id", tenantID)
		c.Set("user_id", uid)
		c.Set("id", uid)
		if u, _ := repo.GetUserById(uid, false); u != nil {
			c.Set("role", u.Role)
		}
		c.Next()
	}
	e := gin.New()
	g := e.Group("/api/v2/:tenant_slug/data-policy")
	g.Use(auth)
	g.GET("/retention", GetContentRetentionV2)
	g.PUT("/retention", PutContentRetentionV2)
	g.PUT("/tokens/:id/retention", PutTokenContentRetentionV2)
	g.GET("/rules", ListContentRulesV2)
	g.POST("/rules", CreateContentRuleV2)
	g.PUT("/rules/:id", UpdateContentRuleV2)
	g.DELETE("/rules/:id", DeleteContentRuleV2)
	a := e.Group("/api/v2/admin")
	a.Use(auth)
	a.POST("/content-rules", CreatePlatformContentRuleV2)
	a.PUT("/content-rules/:id", UpdatePlatformContentRuleV2)
	a.GET("/content-rules", ListPlatformContentRulesV2)
	a.GET("/channel-templates", ListChannelTemplatesV2)
	a.POST("/channel-templates", CreateChannelTemplateV2)
	a.PUT("/channel-templates/:id", UpdateChannelTemplateV2)
	a.DELETE("/channel-templates/:id", DeleteChannelTemplateV2)
	a.POST("/channel-templates/:id/apply", ApplyChannelTemplateV2)

	t.Cleanup(func() {
		repo.InvalidateContentRulesCache()
		constant.MaxRequestBodyMB = prevMaxBody
		repo.InvalidateContentRetentionCache()
		contentpolicy.SetPlatformDefault(nil)
		ctx.Cleanup()
	})
	return &dpFixture{V2TestContext: ctx, engine: e}
}

func (f *dpFixture) do(user *repo.User, method, path string, body interface{}, roles ...string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User-ID", strconv.Itoa(user.Id))
	if len(roles) > 0 {
		req.Header.Set("X-Test-Roles", strings.Join(roles, ","))
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func (f *dpFixture) base() string { return "/api/v2/" + f.TenantID + "/data-policy" }

func dpErrCode(w *httptest.ResponseRecorder) string {
	return gjson.Get(w.Body.String(), "error_code").String()
}

// ---- retention ---------------------------------------------------------

func TestRetentionV2_AdminTightensMemberDenied(t *testing.T) {
	f := setupDataPolicy(t)
	if w := f.do(f.NormalUser, http.MethodPut, f.base()+"/retention", map[string]string{"content_retention": "none"}); w.Code != http.StatusForbidden {
		t.Fatalf("member write: %d %s", w.Code, w.Body)
	}
	if w := f.do(f.NormalUser, http.MethodGet, f.base()+"/retention", nil); w.Code != http.StatusForbidden {
		t.Fatalf("member read: %d", w.Code)
	}
	w := f.do(f.AdminUser, http.MethodPut, f.base()+"/retention", map[string]string{"content_retention": "metadata_only"}, "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("admin write: %d %s", w.Code, w.Body)
	}
	got, _ := repo.GetTenantContentRetention(f.TenantID)
	if got != contentpolicy.RetentionMetadataOnly {
		t.Fatalf("stored %q", got)
	}
	r := f.do(f.AdminUser, http.MethodGet, f.base()+"/retention", nil, "admin")
	if gjson.Get(r.Body.String(), "data.effective").String() != "metadata_only" {
		t.Fatalf("view: %s", r.Body)
	}
	if w := f.do(f.AdminUser, http.MethodPut, f.base()+"/retention", map[string]string{"content_retention": "weird"}, "admin"); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: %d", w.Code)
	}
}

func TestRetentionV2_TenantCannotLoosenPlatformDefault(t *testing.T) {
	f := setupDataPolicy(t)
	m := contentpolicy.RetentionMetadataOnly
	contentpolicy.SetPlatformDefault(&m)
	w := f.do(f.AdminUser, http.MethodPut, f.base()+"/retention", map[string]string{"content_retention": "full"}, "admin")
	if w.Code != http.StatusBadRequest || dpErrCode(w) != "RETENTION_CANNOT_LOOSEN" {
		t.Fatalf("loosening below the platform default accepted: %d %s", w.Code, w.Body)
	}
	if w := f.do(f.AdminUser, http.MethodPut, f.base()+"/retention", map[string]string{"content_retention": "none"}, "admin"); w.Code != http.StatusOK {
		t.Fatalf("tightening refused: %d %s", w.Code, w.Body)
	}
}

func TestTokenRetentionV2_CrossTenantIs404(t *testing.T) {
	f := setupDataPolicy(t)
	foreign := SeedV2Token(t, f.V2TestContext, f.NormalUser.Id, "foreign")
	if err := f.DB.Model(&repo.Token{}).Where("id = ?", foreign.Id).Update("tenant_id", "other-tenant").Error; err != nil {
		t.Fatal(err)
	}
	w := f.do(f.AdminUser, http.MethodPut, fmt.Sprintf("%s/tokens/%d/retention", f.base(), foreign.Id),
		map[string]string{"content_retention": "none"}, "admin")
	if w.Code != http.StatusNotFound {
		t.Fatalf("another tenant's token was addressable: %d %s", w.Code, w.Body)
	}
	got, _ := repo.GetTokenContentRetention(foreign.Id)
	if got != "" {
		t.Fatalf("foreign token modified: %q", got)
	}
}

func TestTokenRetentionV2_TightenOnlyAndEffective(t *testing.T) {
	f := setupDataPolicy(t)
	tok := SeedV2Token(t, f.V2TestContext, f.NormalUser.Id, "own")
	path := fmt.Sprintf("%s/tokens/%d/retention", f.base(), tok.Id)
	if err := repo.SetTenantContentRetention(f.TenantID, contentpolicy.RetentionMetadataOnly); err != nil {
		t.Fatal(err)
	}
	w := f.do(f.AdminUser, http.MethodPut, path, map[string]string{"content_retention": "full"}, "admin")
	if w.Code != http.StatusBadRequest || dpErrCode(w) != "RETENTION_CANNOT_LOOSEN" {
		t.Fatalf("token loosened its tenant: %d %s", w.Code, w.Body)
	}
	w = f.do(f.AdminUser, http.MethodPut, path, map[string]string{"content_retention": "none"}, "admin")
	if w.Code != http.StatusOK || gjson.Get(w.Body.String(), "data.effective").String() != "none" {
		t.Fatalf("tighten: %d %s", w.Code, w.Body)
	}
	if w := f.do(f.NormalUser, http.MethodPut, path, map[string]string{"content_retention": "none"}); w.Code != http.StatusForbidden {
		t.Fatalf("member set token retention: %d", w.Code)
	}
}

// ---- content rules -----------------------------------------------------

func ruleBody(kind, mode, builtin string) map[string]interface{} {
	return map[string]interface{}{"kind": kind, "mode": mode, "pattern_type": "builtin", "builtin": builtin, "role_scope": "any"}
}

func TestContentRuleV2_TenantAdminCRUDStampsOwnTenant(t *testing.T) {
	f := setupDataPolicy(t)
	body := ruleBody("mask", "enforce", "phone_cn")
	body["tenant_id"] = "someone-else" // must be ignored
	body["scope"] = "platform"         // must be ignored
	w := f.do(f.AdminUser, http.MethodPost, f.base()+"/rules", body, "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	id := gjson.Get(w.Body.String(), "data.id").Int()
	r, err := repo.GetContentRule(id, "tenant", f.TenantID)
	if err != nil || r.Scope != "tenant" || r.TenantId != f.TenantID {
		t.Fatalf("rule not stamped with the caller's tenant: %+v %v", r, err)
	}
	if w := f.do(f.NormalUser, http.MethodPost, f.base()+"/rules", ruleBody("mask", "observe", "email")); w.Code != http.StatusForbidden {
		t.Fatalf("member created a rule: %d", w.Code)
	}
	upd := ruleBody("mask", "observe", "phone_cn")
	if w := f.do(f.AdminUser, http.MethodPut, fmt.Sprintf("%s/rules/%d", f.base(), id), upd, "admin"); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if w := f.do(f.AdminUser, http.MethodDelete, fmt.Sprintf("%s/rules/%d", f.base(), id), nil, "admin"); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
}

func TestContentRuleV2_CrossTenantIs404(t *testing.T) {
	f := setupDataPolicy(t)
	foreign := &repo.ContentRule{Scope: "tenant", TenantId: "other-tenant", RoleScope: "any", Kind: "mask",
		PatternType: "builtin", Builtin: "email", Mode: "enforce", Enabled: true}
	platform := &repo.ContentRule{Scope: "platform", RoleScope: "any", Kind: "mask",
		PatternType: "builtin", Builtin: "email", Mode: "enforce", Enabled: true}
	for _, r := range []*repo.ContentRule{foreign, platform} {
		if err := repo.CreateContentRule(r); err != nil {
			t.Fatal(err)
		}
	}
	for name, id := range map[string]int64{"foreign tenant rule": foreign.Id, "platform rule": platform.Id} {
		put := f.do(f.AdminUser, http.MethodPut, fmt.Sprintf("%s/rules/%d", f.base(), id), ruleBody("reject", "enforce", "email"), "admin")
		del := f.do(f.AdminUser, http.MethodDelete, fmt.Sprintf("%s/rules/%d", f.base(), id), nil, "admin")
		if put.Code != http.StatusNotFound || del.Code != http.StatusNotFound {
			t.Errorf("%s reachable through a tenant route: put=%d delete=%d", name, put.Code, del.Code)
		}
	}
	got, _ := repo.GetContentRule(foreign.Id, "tenant", "other-tenant")
	if got == nil || got.Kind != "mask" {
		t.Fatalf("foreign rule modified: %+v", got)
	}
	if p, _ := repo.GetContentRule(platform.Id, "platform", ""); p == nil {
		t.Fatal("platform rule deleted by a tenant admin")
	}
	// the tenant list only shows its own rules
	l := f.do(f.AdminUser, http.MethodGet, f.base()+"/rules", nil, "admin")
	if n := len(gjson.Get(l.Body.String(), "data.rules").Array()); n != 0 {
		t.Fatalf("tenant list leaked %d foreign/platform rules: %s", n, l.Body)
	}
}

func TestContentRuleV2_ValidationErrors(t *testing.T) {
	f := setupDataPolicy(t)
	bad := []map[string]interface{}{
		{"kind": "mask", "mode": "enforce", "pattern_type": "regex", "pattern": "("},
		{"kind": "mask", "mode": "enforce", "pattern_type": "regex", "pattern": strings.Repeat("a", 513)},
		{"kind": "mask", "mode": "enforce", "pattern_type": "regex", "pattern": "a*"},
		{"kind": "mask", "mode": "enforce", "pattern_type": "builtin", "builtin": "nope"},
		{"kind": "explode", "mode": "enforce", "pattern_type": "builtin", "builtin": "email"},
	}
	for i, b := range bad {
		w := f.do(f.AdminUser, http.MethodPost, f.base()+"/rules", b, "admin")
		if w.Code != http.StatusBadRequest || dpErrCode(w) != "INVALID_RULE" {
			t.Errorf("case %d accepted: %d %s", i, w.Code, w.Body)
		}
	}
}

func TestPlatformContentRule_ScopeFixedToPlatform(t *testing.T) {
	f := setupDataPolicy(t)
	body := ruleBody("reject", "observe", "bank_card")
	body["scope"] = "tenant"
	body["tenant_id"] = f.TenantID
	w := f.do(f.RootUser, http.MethodPost, "/api/v2/admin/content-rules", body, "root")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	id := gjson.Get(w.Body.String(), "data.id").Int()
	if r, err := repo.GetContentRule(id, "platform", ""); err != nil || r.TenantId != "" {
		t.Fatalf("platform route produced a non-platform rule: %+v %v", r, err)
	}
}

// ---- override templates ------------------------------------------------

func TestChannelTemplatesV2_ValidateApplyAudit(t *testing.T) {
	f := setupDataPolicy(t)
	ch := SeedV2Channel(t, f.V2TestContext, "tpl-target")
	bad := map[string]string{"name": "bad", "param_override": "{not json"}
	if w := f.do(f.RootUser, http.MethodPost, "/api/v2/admin/channel-templates", bad, "root"); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid override accepted: %d %s", w.Code, w.Body)
	}
	empty := map[string]string{"name": "empty"}
	if w := f.do(f.RootUser, http.MethodPost, "/api/v2/admin/channel-templates", empty, "root"); w.Code != http.StatusBadRequest {
		t.Fatalf("empty template accepted: %d", w.Code)
	}
	good := map[string]string{"name": "ok", "param_override": `{"temperature":0.3}`, "header_override": `{"X-Tpl":"1"}`}
	w := f.do(f.RootUser, http.MethodPost, "/api/v2/admin/channel-templates", good, "root")
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	id := gjson.Get(w.Body.String(), "data.id").Int()
	if w := f.do(f.RootUser, http.MethodPost, "/api/v2/admin/channel-templates", good, "root"); w.Code != http.StatusConflict {
		t.Fatalf("duplicate name: %d", w.Code)
	}
	ap := f.do(f.RootUser, http.MethodPost, fmt.Sprintf("/api/v2/admin/channel-templates/%d/apply", id),
		map[string][]int{"channel_ids": {ch.Id, 424242}}, "root")
	if ap.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", ap.Code, ap.Body)
	}
	res := gjson.Get(ap.Body.String(), "data.results").Array()
	if len(res) != 2 || !res[0].Get("applied").Bool() || res[1].Get("applied").Bool() {
		t.Fatalf("results: %s", ap.Body)
	}
	app, _ := repo.LatestTemplateApplication(ch.Id)
	if app == nil || app.TemplateId != id || app.TemplateVersion != 1 {
		t.Fatalf("source template not recorded: %+v", app)
	}
	if w := f.do(f.RootUser, http.MethodPost, fmt.Sprintf("/api/v2/admin/channel-templates/%d/apply", id),
		map[string][]int{"channel_ids": {}}, "root"); w.Code != http.StatusBadRequest {
		t.Fatalf("empty channel list: %d", w.Code)
	}
}

// ---- relay entry: applyContentRules -----------------------------------

var relayBodies = map[types.RelayFormat]string{
	types.RelayFormatOpenAI:          `{"model":"model-a","messages":[{"role":"user","content":"tel ` + dpPhone + `"}]}`,
	types.RelayFormatOpenAIResponses: `{"model":"model-a","input":[{"role":"user","content":[{"type":"input_text","text":"tel ` + dpPhone + `"}]}]}`,
	types.RelayFormatClaude:          `{"model":"model-a","max_tokens":5,"messages":[{"role":"user","content":"tel ` + dpPhone + `"}]}`,
	types.RelayFormatGemini:          `{"contents":[{"role":"user","parts":[{"text":"tel ` + dpPhone + `"}]}]}`,
}

func relayCtx(tenantID, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("tenant_id", tenantID)
	return c, w
}

func seedRule(t *testing.T, scope, tenant, kind, mode, builtin string) *repo.ContentRule {
	t.Helper()
	r := &repo.ContentRule{Scope: scope, TenantId: tenant, RoleScope: "any", Kind: kind, PatternType: "builtin",
		Builtin: builtin, Mode: mode, Enabled: true}
	if err := repo.CreateContentRule(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyContentRules_MasksEveryFormatAndPassThroughSeesIt(t *testing.T) {
	f := setupDataPolicy(t)
	seedRule(t, "tenant", f.TenantID, "mask", "enforce", "phone_cn")
	for format, body := range relayBodies {
		c, _ := relayCtx(f.TenantID, body)
		if err := applyContentRules(c, format); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		// The pass-through path forwards exactly what common.GetRequestBody
		// returns (compatible/claude/gemini/responses handlers); the typed path
		// parses the same buffer. Both must see the redacted bytes.
		forwarded, _ := common.GetRequestBody(c)
		if strings.Contains(string(forwarded), dpPhone) || !strings.Contains(string(forwarded), "[PHONE]") {
			t.Errorf("%s: pass-through would forward the original: %s", format, forwarded)
		}
		next := new(bytes.Buffer)
		next.ReadFrom(c.Request.Body)
		if strings.Contains(next.String(), dpPhone) {
			t.Errorf("%s: a late re-read of Request.Body saw the original", format)
		}
	}
}

func TestApplyContentRules_RejectIs400WithRuleIDNoContent(t *testing.T) {
	f := setupDataPolicy(t)
	r := seedRule(t, "tenant", f.TenantID, "reject", "enforce", "phone_cn")
	for format, body := range relayBodies {
		c, _ := relayCtx(f.TenantID, body)
		err := applyContentRules(c, format)
		if err == nil {
			t.Fatalf("%s: not rejected", format)
		}
		if err.GetErrorCode() != types.ErrorCodeContentRejected || err.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: code=%s status=%d", format, err.GetErrorCode(), err.StatusCode)
		}
		msg := err.Error()
		if !strings.Contains(msg, strconv.FormatInt(r.Id, 10)) || strings.Contains(msg, dpPhone) {
			t.Errorf("%s: message must carry the rule id and never the content: %q", format, msg)
		}
		if fmt.Sprint(err.ToOpenAIError().Code) != "content_rejected" {
			t.Errorf("%s: wire code %v", format, err.ToOpenAIError().Code)
		}
	}
}

func TestApplyContentRules_ObserveDoesNotChangeRequest(t *testing.T) {
	f := setupDataPolicy(t)
	seedRule(t, "tenant", f.TenantID, "mask", "observe", "phone_cn")
	seedRule(t, "tenant", f.TenantID, "reject", "observe", "phone_cn")
	for format, body := range relayBodies {
		c, _ := relayCtx(f.TenantID, body)
		if err := applyContentRules(c, format); err != nil {
			t.Fatalf("%s: observe rejected: %v", format, err)
		}
		got, _ := common.GetRequestBody(c)
		if string(got) != body {
			t.Errorf("%s: observe altered the body: %s", format, got)
		}
	}
}

func TestApplyContentRules_TenantIsolationAndPlatformRules(t *testing.T) {
	_ = setupDataPolicy(t)
	seedRule(t, "tenant", "tenant-a", "reject", "enforce", "phone_cn")
	// tenant B is untouched by tenant A's rule
	c, _ := relayCtx("tenant-b", relayBodies[types.RelayFormatOpenAI])
	if err := applyContentRules(c, types.RelayFormatOpenAI); err != nil {
		t.Fatalf("tenant-b hit tenant-a's rule: %v", err)
	}
	// a platform rule reaches every tenant
	seedRule(t, "platform", "", "mask", "enforce", "phone_cn")
	c, _ = relayCtx("tenant-b", relayBodies[types.RelayFormatOpenAI])
	if err := applyContentRules(c, types.RelayFormatOpenAI); err != nil {
		t.Fatal(err)
	}
	if got, _ := common.GetRequestBody(c); strings.Contains(string(got), dpPhone) {
		t.Fatal("platform rule did not apply to tenant-b")
	}
}

func TestApplyContentRules_NoRulesAndUncoveredFormatsUntouched(t *testing.T) {
	f := setupDataPolicy(t)
	c, _ := relayCtx(f.TenantID, relayBodies[types.RelayFormatOpenAI])
	if err := applyContentRules(c, types.RelayFormatOpenAI); err != nil {
		t.Fatal(err)
	}
	if _, set := c.Get(common.KeyRequestBody); set {
		t.Error("no rules: the body must not even be buffered")
	}
	seedRule(t, "tenant", f.TenantID, "reject", "enforce", "phone_cn")
	c, _ = relayCtx(f.TenantID, relayBodies[types.RelayFormatOpenAI])
	if err := applyContentRules(c, types.RelayFormatEmbedding); err != nil {
		t.Fatalf("embedding format is outside the rules' scope: %v", err)
	}
}

func TestApplyContentRules_WiredBeforeParsing(t *testing.T) {
	src, err := os.ReadFile("relay.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	rules := strings.Index(s, "applyContentRules(c, relayFormat)")
	parse := strings.Index(s, "helper.GetAndValidateRequest(c, relayFormat)")
	if rules < 0 || parse < 0 || rules > parse {
		t.Fatalf("content rules must run before the request is parsed (rules@%d parse@%d)", rules, parse)
	}
}
