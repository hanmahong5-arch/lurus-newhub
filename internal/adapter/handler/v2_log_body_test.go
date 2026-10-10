package handler

// v2_log_body_test.go - migration 052 opt-in body archive API: the tenant
// consent bit and the body read.
//
// Test names referenced by router/v2_completeness_test.go's swept map:
//   TestLogBodyV2_CrossTenantIs404

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func setupLogBody(t *testing.T) *dpFixture {
	t.Helper()
	f := setupDataPolicy(t)
	if err := f.DB.AutoMigrate(&repo.LogBody{}); err != nil {
		t.Fatal(err)
	}
	repo.InvalidateSedimentationConsentCache()
	// A dedicated engine: setupDataPolicy's auth stub is a closure over its own
	// groups, so the new routes get the same stub here.
	auth := func(c *gin.Context) {
		tenantID := c.GetHeader("X-Test-Tenant-ID")
		if tenantID == "" {
			tenantID = f.TenantID
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
	e.Use(auth)
	e.GET("/api/v2/:tenant_slug/data-policy/sedimentation", GetSedimentationConsentV2)
	e.PUT("/api/v2/:tenant_slug/data-policy/sedimentation", PutSedimentationConsentV2)
	e.GET("/api/v2/:tenant_slug/logs/:request_id/body", GetLogBodyV2)
	e.GET("/api/v2/admin/logs/:request_id/body", GetAdminLogBodyV2)
	f.engine = e
	t.Cleanup(repo.InvalidateSedimentationConsentCache)
	return f
}

func seedBody(t *testing.T, f *dpFixture, tenant, rid string, expires int64) {
	t.Helper()
	if err := f.DB.Create(&repo.LogBody{
		RequestId: rid, TenantId: tenant, UserId: 7, TokenId: 9, Model: "model-a",
		CreatedAt: common.GetTimestamp(), ExpiresAt: expires,
		RequestBody: `{"p":"masked"}`, ResponseText: "answer", ResponseCaptured: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSedimentationConsentV2_DefaultOffAdminTogglesMemberDenied(t *testing.T) {
	f := setupLogBody(t)
	path := f.base() + "/sedimentation"

	r := f.do(f.AdminUser, http.MethodGet, path, nil, "admin")
	if r.Code != http.StatusOK || gjson.Get(r.Body.String(), "data.consent").Bool() {
		t.Fatalf("default must be off: %d %s", r.Code, r.Body)
	}
	if w := f.do(f.NormalUser, http.MethodPut, path, map[string]bool{"consent": true}); w.Code != http.StatusForbidden {
		t.Fatalf("member write: %d", w.Code)
	}
	if w := f.do(f.NormalUser, http.MethodGet, path, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member read: %d", w.Code)
	}
	if w := f.do(f.AdminUser, http.MethodPut, path, map[string]string{"consent": "yes"}, "admin"); w.Code != http.StatusBadRequest {
		t.Fatalf("non-boolean accepted: %d", w.Code)
	}
	if w := f.do(f.AdminUser, http.MethodPut, path, map[string]string{}, "admin"); w.Code != http.StatusBadRequest {
		t.Fatalf("missing consent accepted: %d", w.Code)
	}

	w := f.do(f.AdminUser, http.MethodPut, path, map[string]bool{"consent": true}, "admin")
	if w.Code != http.StatusOK || !gjson.Get(w.Body.String(), "data.consent").Bool() {
		t.Fatalf("admin on: %d %s", w.Code, w.Body)
	}
	if on, _ := repo.GetTenantSedimentationConsent(f.TenantID); !on {
		t.Fatal("not stored")
	}
	if w := f.do(f.AdminUser, http.MethodPut, path, map[string]bool{"consent": false}, "admin"); w.Code != http.StatusOK {
		t.Fatalf("withdraw: %d", w.Code)
	}
	if on, _ := repo.GetTenantSedimentationConsent(f.TenantID); on {
		t.Fatal("withdrawal not stored")
	}
}

func TestLogBodyV2_CrossTenantIs404(t *testing.T) {
	f := setupLogBody(t)
	seedBody(t, f, f.TenantID, "rid-own", common.GetTimestamp()+1000)
	seedBody(t, f, "other-tenant", "rid-foreign", common.GetTimestamp()+1000)
	own := "/api/v2/" + f.TenantID + "/logs/"

	w := f.do(f.AdminUser, http.MethodGet, own+"rid-own/body", nil, "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("own read: %d %s", w.Code, w.Body)
	}
	for k, want := range map[string]string{
		"data.request_body": `{"p":"masked"}`, "data.response_text": "answer",
		"data.model": "model-a", "data.tenant_id": f.TenantID,
	} {
		if got := gjson.Get(w.Body.String(), k).String(); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !gjson.Get(w.Body.String(), "data.response_captured").Bool() || gjson.Get(w.Body.String(), "data.truncated").Bool() {
		t.Errorf("flags wrong: %s", w.Body)
	}

	foreign := f.do(f.AdminUser, http.MethodGet, own+"rid-foreign/body", nil, "admin")
	missing := f.do(f.AdminUser, http.MethodGet, own+"rid-nope/body", nil, "admin")
	if foreign.Code != http.StatusNotFound || dpErrCode(foreign) != "LOG_BODY_NOT_FOUND" {
		t.Fatalf("another tenant's body readable: %d %s", foreign.Code, foreign.Body)
	}
	// Indistinguishable from a request id that does not exist at all.
	if foreign.Body.String() != missing.Body.String() || foreign.Code != missing.Code {
		t.Fatalf("existence leak: foreign=%q missing=%q", foreign.Body, missing.Body)
	}
	if gjson.Get(foreign.Body.String(), "data").Exists() {
		t.Fatal("foreign 404 carried data")
	}
	if w := f.do(f.NormalUser, http.MethodGet, own+"rid-own/body", nil); w.Code != http.StatusForbidden {
		t.Fatalf("member read: %d", w.Code)
	}
}

func TestLogBodyV2_ExpiredIs404(t *testing.T) {
	f := setupLogBody(t)
	seedBody(t, f, f.TenantID, "rid-old", common.GetTimestamp()-1)
	w := f.do(f.AdminUser, http.MethodGet, "/api/v2/"+f.TenantID+"/logs/rid-old/body", nil, "admin")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expired body served: %d %s", w.Code, w.Body)
	}
}

func TestLogBodyV2_AdminRouteReadsAnyTenant(t *testing.T) {
	f := setupLogBody(t)
	seedBody(t, f, "other-tenant", "rid-foreign", common.GetTimestamp()+1000)
	w := f.do(f.AdminUser, http.MethodGet, "/api/v2/admin/logs/rid-foreign/body", nil, "admin")
	if w.Code != http.StatusOK || gjson.Get(w.Body.String(), "data.tenant_id").String() != "other-tenant" {
		t.Fatalf("platform read: %d %s", w.Code, w.Body)
	}
	if w := f.do(f.AdminUser, http.MethodGet, "/api/v2/admin/logs/rid-nope/body", nil, "admin"); w.Code != http.StatusNotFound {
		t.Fatalf("miss: %d", w.Code)
	}
}

// The repo gate may only archive formats the content rules actually inspect;
// contentFormatFor is the handler-side source of truth for that set.
func TestLogBodyFormatCovered_MatchesContentRuleFormats(t *testing.T) {
	all := []types.RelayFormat{
		types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini,
		types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompact,
		types.RelayFormatOpenAIAudio, types.RelayFormatOpenAIImage, types.RelayFormatOpenAIRealtime,
		types.RelayFormatRerank, types.RelayFormatEmbedding, types.RelayFormatSystemOne,
		types.RelayFormatTask, types.RelayFormatMjProxy, "",
	}
	for _, f := range all {
		_, covered := contentFormatFor(f)
		if got := repo.LogBodyFormatCovered(f); got != covered {
			t.Errorf("format %q: archive covered=%v but content rules covered=%v", f, got, covered)
		}
	}
}

// Turning consent off deletes the tenant's archived bodies in the same
// request and says how many went; other tenants' rows are untouched.
func TestSedimentationConsentV2_WithdrawalPurgesOwnBodies(t *testing.T) {
	f := setupLogBody(t)
	path := f.base() + "/sedimentation"
	seedBody(t, f, f.TenantID, "rid-1", common.GetTimestamp()+1000)
	seedBody(t, f, f.TenantID, "rid-2", common.GetTimestamp()+1000)
	seedBody(t, f, "other-tenant", "rid-foreign", common.GetTimestamp()+1000)

	if w := f.do(f.AdminUser, http.MethodPut, path, map[string]bool{"consent": true}, "admin"); w.Code != http.StatusOK {
		t.Fatalf("on: %d %s", w.Code, w.Body)
	}
	w := f.do(f.AdminUser, http.MethodPut, path, map[string]bool{"consent": false}, "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", w.Code, w.Body)
	}
	if got := gjson.Get(w.Body.String(), "data.purged_bodies").Int(); got != 2 {
		t.Fatalf("purged_bodies = %d, want 2: %s", got, w.Body)
	}
	if gjson.Get(w.Body.String(), "data.purge_pending").Bool() {
		t.Fatalf("purge_pending set on a clean purge: %s", w.Body)
	}
	count := func(tenant string) int64 {
		var n int64
		if err := f.DB.Model(&repo.LogBody{}).Where("tenant_id = ?", tenant).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(f.TenantID); n != 0 {
		t.Fatalf("own bodies left after withdrawal: %d", n)
	}
	if n := count("other-tenant"); n != 1 {
		t.Fatalf("another tenant's body touched: %d left", n)
	}
	if w := f.do(f.AdminUser, http.MethodGet, "/api/v2/"+f.TenantID+"/logs/rid-1/body", nil, "admin"); w.Code != http.StatusNotFound {
		t.Fatalf("purged body still served: %d %s", w.Code, w.Body)
	}
	// Turning it on again carries no purge fields.
	if w := f.do(f.AdminUser, http.MethodPut, path, map[string]bool{"consent": true}, "admin"); gjson.Get(w.Body.String(), "data.purged_bodies").Exists() {
		t.Fatalf("purge fields on enable: %s", w.Body)
	}
}
