package handler

// Lane A4: tenant audit trail + tenant-admin model allow-list self-service.
// Covers privilege (member / dept_lead denied, tenant admin + staff allowed),
// cross-tenant isolation, narrow-only semantics and detail redaction.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func a4Router(ctx *V2TestContext, tenantRole string, staff bool, method, path string, h gin.HandlerFunc) *gin.Engine {
	role := common.RoleCommonUser
	if staff {
		role = common.RoleAdminUser
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", role)
		c.Set("id", ctx.NormalUser.Id)
		c.Set("tenant_id", ctx.TenantID)
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: ctx.TenantID, UserID: ctx.NormalUser.Id,
			RoleTenantID: ctx.TenantID, TenantRole: tenantRole, Roles: []string{},
		})
	})
	r.Handle(method, path, h)
	return r
}

func a4Do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func a4SeedAudit(t *testing.T, tenant, action, details string, ts int64) {
	t.Helper()
	if err := repo.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	e := &entity.AuditEvent{TenantID: tenant, Timestamp: ts, ActorType: "user", ActorID: 7, Action: action, Resource: "tenant", Details: details}
	if err := repo.DB.Create(e).Error; err != nil {
		t.Fatal(err)
	}
}

func TestTenantAudit_Privilege(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	a4Migrate(t)
	for _, tc := range []struct {
		role  string
		staff bool
		want  int
	}{
		{"", false, 403}, {entity.TenantRoleDeptLead, false, 403},
		{entity.TenantRoleAdmin, false, 200}, {"", true, 200},
	} {
		for _, h := range []struct {
			name string
			fn   gin.HandlerFunc
		}{{"list", ListTenantAuditV2}, {"csv", ExportTenantAuditCSVV2}} {
			w := a4Do(a4Router(ctx, tc.role, tc.staff, "GET", "/x", h.fn), "GET", "/x", "")
			if w.Code != tc.want {
				t.Errorf("%s role=%q staff=%v: got %d want %d", h.name, tc.role, tc.staff, w.Code, tc.want)
			}
		}
	}
}

func TestTenantAudit_CrossTenantAndFilters(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	now := time.Now().Unix()
	a4SeedAudit(t, ctx.TenantID, "token.created", `{"name":"mine"}`, now-100)
	a4SeedAudit(t, ctx.TenantID, "tenant.updated", `{"name":"mine2"}`, now-50)
	a4SeedAudit(t, "other-tenant", "token.created", `{"name":"THEIRS"}`, now-10)

	r := a4Router(ctx, entity.TenantRoleAdmin, false, "GET", "/x", ListTenantAuditV2)
	w := a4Do(r, "GET", "/x", "")
	var resp struct {
		Data struct {
			Items []tenantAuditView `json:"items"`
			Total int64             `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Total != 2 || len(resp.Data.Items) != 2 || strings.Contains(w.Body.String(), "THEIRS") {
		t.Fatalf("tenant isolation broken: %s", w.Body.String())
	}
	// A hostile tenant_id query param must be ignored.
	w = a4Do(r, "GET", "/x?tenant_id=other-tenant", "")
	if strings.Contains(w.Body.String(), "THEIRS") {
		t.Fatalf("tenant_id param honoured: %s", w.Body.String())
	}
	// action filter
	w = a4Do(r, "GET", "/x?action=tenant.updated", "")
	if !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("action filter: %s", w.Body.String())
	}
	if w = a4Do(r, "GET", "/x?action=bogus.action", ""); w.Code != 400 {
		t.Fatalf("unknown action: %d", w.Code)
	}
	// time filter
	w = a4Do(r, "GET", "/x?start_time="+itoa64(now-60), "")
	if !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("time filter: %s", w.Body.String())
	}
	// CSV is tenant-pinned too
	w = a4Do(a4Router(ctx, entity.TenantRoleAdmin, false, "GET", "/x", ExportTenantAuditCSVV2), "GET", "/x", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "THEIRS") || !strings.Contains(w.Body.String(), "mine2") {
		t.Fatalf("csv: %d %s", w.Code, w.Body.String())
	}
	if _, _, err := repo.ListTenantAuditEvents("", "", 0, 0, 0, 0, 10); err == nil {
		t.Fatal("empty tenant must be rejected")
	}
	if _, _, err := repo.ExportTenantAuditEvents("", 0, "", 0, 0, 0, 10); err == nil {
		t.Fatal("empty tenant must be rejected for export")
	}
}

func itoa64(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestTenantAudit_Redaction(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	a4SeedAudit(t, ctx.TenantID, "tenant.updated",
		`{"channel_id":42,"upstream_key":"sk-abcdef123456789","internal_note":"vip","base_url":"http://10.0.0.1","name":"ok","nested":{"remark":"x","msg":"used channel 77 with sk-zzzzzzzz1234"}}`,
		time.Now().Unix())
	a4SeedAudit(t, ctx.TenantID, "tenant.updated", "rotated channel #9 key sk-live_ABCDEFGH12345", time.Now().Unix())

	// Leak probes must be specific: a bare ":42" also matches the response's own
	// auto-increment row id ("id":42, 420..429, 4200...) once the shared audit
	// table has grown past that in a full-package run.
	for _, h := range []gin.HandlerFunc{ListTenantAuditV2, ExportTenantAuditCSVV2} {
		w := a4Do(a4Router(ctx, entity.TenantRoleAdmin, false, "GET", "/x", h), "GET", "/x", "")
		body := w.Body.String()
		for _, leak := range []string{"abcdef123456789", "vip", "10.0.0.1", "zzzzzzzz1234", "ABCDEFGH12345", `channel_id\":42`, `"channel_id":42`, "channel 77", "channel #9", "prev_hash", "row_hash"} {
			if strings.Contains(body, leak) {
				t.Errorf("leaked %q in %s", leak, body)
			}
		}
		if !strings.Contains(body, "ok") {
			t.Errorf("benign field lost: %s", body)
		}
	}
}

func a4SeedPlatformList(t *testing.T, tenant string, list []string) {
	t.Helper()
	if err := repo.SetTenantConfigJSON(tenant, tenantpolicy.ModelAllowlistConfigKey, list, ""); err != nil {
		t.Fatal(err)
	}
	tenantpolicy.Invalidate(tenant)
}

func TestTenantModelAllowlist_PrivilegeAndNarrowOnly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	a4SeedPlatformList(t, ctx.TenantID, []string{"gpt-4o", "claude-*"})
	t.Cleanup(func() { tenantpolicy.InvalidateSelection(ctx.TenantID) })
	body := `{"selected_models":["gpt-4o"]}`

	// member / dept_lead cannot read or write
	for _, role := range []string{"", entity.TenantRoleDeptLead} {
		if w := a4Do(a4Router(ctx, role, false, "PUT", "/x", PutTenantModelAllowlistV2), "PUT", "/x", body); w.Code != 403 {
			t.Errorf("role %q PUT got %d", role, w.Code)
		}
		if w := a4Do(a4Router(ctx, role, false, "GET", "/x", GetTenantModelAllowlistV2), "GET", "/x", ""); w.Code != 403 {
			t.Errorf("role %q GET got %d", role, w.Code)
		}
	}
	if _, ok, _ := tenantpolicy.LoadSelectionUncached(ctx.TenantID); ok {
		t.Fatal("denied PUT must not write")
	}

	put := a4Router(ctx, entity.TenantRoleAdmin, false, "PUT", "/x", PutTenantModelAllowlistV2)
	// widening beyond the platform grant is refused, nothing persisted
	for _, bad := range []string{`{"selected_models":["gpt-4o","gemini-pro"]}`, `{"selected_models":["gpt-*"]}`, `{"selected_models":["*"]}`} {
		if w := a4Do(put, "PUT", "/x", bad); w.Code != 403 || !strings.Contains(w.Body.String(), "MODEL_NOT_GRANTED") {
			t.Errorf("widening %s got %d %s", bad, w.Code, w.Body.String())
		}
	}
	if _, ok, _ := tenantpolicy.LoadSelectionUncached(ctx.TenantID); ok {
		t.Fatal("refused PUT must not write")
	}
	if w := a4Do(put, "PUT", "/x", `{}`); w.Code != 400 {
		t.Errorf("missing field got %d", w.Code)
	}

	// narrowing works and is audited
	cap := &captureAuditWriter{}
	setCaptureAuditWriter(t, cap)
	if w := a4Do(put, "PUT", "/x", `{"selected_models":["gpt-4o","claude-3*"]}`); w.Code != 200 {
		t.Fatalf("narrow got %d %s", w.Code, w.Body.String())
	}
	if !cap.waitFor("model-selection") {
		t.Error("write was not audited")
	}

	w := a4Do(a4Router(ctx, entity.TenantRoleAdmin, false, "GET", "/x", GetTenantModelAllowlistV2), "GET", "/x", "")
	var resp struct {
		Data struct {
			PlatformAllowed []string `json:"platform_allowed"`
			TenantSelected  []string `json:"tenant_selected"`
			Effective       []string `json:"effective"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if strings.Join(resp.Data.PlatformAllowed, ",") != "gpt-4o,claude-*" ||
		strings.Join(resp.Data.TenantSelected, ",") != "gpt-4o,claude-3*" ||
		strings.Join(resp.Data.Effective, ",") != "gpt-4o,claude-3*" {
		t.Fatalf("view: %s", w.Body.String())
	}

	// platform later narrows below the selection: effective never exceeds it
	a4SeedPlatformList(t, ctx.TenantID, []string{"gpt-4o"})
	w = a4Do(a4Router(ctx, entity.TenantRoleAdmin, false, "GET", "/x", GetTenantModelAllowlistV2), "GET", "/x", "")
	if !strings.Contains(w.Body.String(), `"effective":["gpt-4o"]`) {
		t.Fatalf("effective after platform narrowing: %s", w.Body.String())
	}
}

func TestTenantModelAllowlist_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := repo.SetTenantConfigJSON("other-tenant", tenantpolicy.SelectionConfigKey, []string{"secret-model"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTenantConfigJSON("other-tenant", tenantpolicy.ModelAllowlistConfigKey, []string{"secret-model"}, ""); err != nil {
		t.Fatal(err)
	}
	w := a4Do(a4Router(ctx, entity.TenantRoleAdmin, false, "GET", "/x", GetTenantModelAllowlistV2), "GET", "/x?tenant=other-tenant", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret-model") {
		t.Fatalf("cross-tenant leak: %d %s", w.Code, w.Body.String())
	}
	// this tenant has no platform row: unrestricted, any narrowing allowed
	w = a4Do(a4Router(ctx, entity.TenantRoleAdmin, false, "PUT", "/x", PutTenantModelAllowlistV2), "PUT", "/x", `{"selected_models":["anything"]}`)
	if w.Code != 200 {
		t.Fatalf("unrestricted tenant narrowing: %d %s", w.Code, w.Body.String())
	}
	if _, ok, _ := tenantpolicy.LoadSelectionUncached("other-tenant"); !ok {
		t.Fatal("other tenant's row should be untouched")
	}
	list, _, _ := tenantpolicy.LoadSelectionUncached("other-tenant")
	if len(list) != 1 || list[0] != "secret-model" {
		t.Fatalf("other tenant mutated: %v", list)
	}
	tenantpolicy.InvalidateSelection(ctx.TenantID)
}

type captureAuditWriter struct {
	mu      sync.Mutex
	details []string
}

func (w *captureAuditWriter) CreateAuditEvent(e *entity.AuditEvent) error {
	w.mu.Lock()
	w.details = append(w.details, e.Details)
	w.mu.Unlock()
	return nil
}

func (w *captureAuditWriter) waitFor(sub string) bool {
	for i := 0; i < 100; i++ {
		w.mu.Lock()
		for _, d := range w.details {
			if strings.Contains(d, sub) {
				w.mu.Unlock()
				return true
			}
		}
		w.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func setCaptureAuditWriter(t *testing.T, w governance.AuditWriter) {
	t.Helper()
	governance.SetAuditWriter(w)
	t.Cleanup(func() { governance.SetAuditWriter(inertAuditWriter{}) })
}

func a4Migrate(t *testing.T) {
	t.Helper()
	if err := repo.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
}
