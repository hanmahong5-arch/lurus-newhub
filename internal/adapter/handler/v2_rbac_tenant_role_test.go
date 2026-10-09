package handler

// v2_rbac_tenant_role_test.go — tenant-scoped roles (users.tenant_role,
// migration 046): requireTenantAdmin, isPlatformStaff, allowedProjectIDs and
// the /logs/all projection for a tenant admin who is NOT platform staff.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func rbacCtx(globalRole int) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("role", globalRole)
	return c
}

func TestRequireTenantAdmin_TenantRole(t *testing.T) {
	cases := []struct {
		name       string
		globalRole int
		tc         *middleware.TenantContext
		want       bool
	}{
		{"tenant admin, same tenant", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "t1", RoleTenantID: "t1", TenantRole: entity.TenantRoleAdmin}, true},
		{"tenant admin of another tenant", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "t2", RoleTenantID: "t1", TenantRole: entity.TenantRoleAdmin}, false},
		{"tenant admin without role tenant (cache miss)", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "t1", TenantRole: entity.TenantRoleAdmin}, false},
		{"dept lead is not admin", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "t1", RoleTenantID: "t1", TenantRole: entity.TenantRoleDeptLead}, false},
		{"plain member", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "t1", RoleTenantID: "t1"}, false},
		// The shared bootstrap tenant hosts every unassigned user, so a role
		// stamped on a "default" row must never be honoured.
		{"tenant admin inside the default tenant", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "default", RoleTenantID: "default", TenantRole: entity.TenantRoleAdmin}, false},
		{"dept lead inside the default tenant", common.RoleCommonUser,
			&middleware.TenantContext{TenantID: "default", RoleTenantID: "default", TenantRole: entity.TenantRoleDeptLead}, false},
		{"global role 10 still admin", common.RoleAdminUser,
			&middleware.TenantContext{TenantID: "t1"}, true},
		{"nil tenant context, common role", common.RoleCommonUser, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requireTenantAdmin(rbacCtx(tc.globalRole), tc.tc); got != tc.want {
				t.Fatalf("requireTenantAdmin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsPlatformStaff_TenantAdminIsNotStaff(t *testing.T) {
	tc := &middleware.TenantContext{TenantID: "t1", RoleTenantID: "t1", TenantRole: entity.TenantRoleAdmin}
	if isPlatformStaff(rbacCtx(common.RoleCommonUser), tc) {
		t.Fatal("a tenant admin must not be platform staff")
	}
	if !isPlatformStaff(rbacCtx(common.RoleAdminUser), tc) {
		t.Fatal("global role 10 is platform staff")
	}
	// An OIDC "admin" claim is tenant-scoped: a tenant admin, never staff, so an
	// IdP that issues it to a customer organisation leaks no supply chain.
	oidcAdmin := &middleware.TenantContext{TenantID: "t1", Roles: []string{"admin"}}
	if isPlatformStaff(rbacCtx(0), oidcAdmin) {
		t.Fatal("an OIDC admin claim must not be platform staff")
	}
	if !requireTenantAdmin(rbacCtx(0), oidcAdmin) {
		t.Fatal("an OIDC admin claim is still a tenant admin")
	}
	oidcRoot := &middleware.TenantContext{TenantID: "t1", Roles: []string{"root"}}
	if !isPlatformStaff(rbacCtx(0), oidcRoot) {
		t.Fatal("an OIDC root claim is platform staff")
	}
}

func TestAllowedProjectIDs_ThreeIdentities(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&entity.ProjectMember{}); err != nil {
		t.Fatalf("migrate project_members: %v", err)
	}
	const tid = "t1"
	// Lead is in two live projects of t1, and one project of ANOTHER tenant.
	mk := func(tenant, name string) int {
		p := &entity.Project{TenantId: tenant, Name: name}
		if err := ctx.DB.Create(p).Error; err != nil {
			t.Fatalf("seed project: %v", err)
		}
		return p.Id
	}
	p1, p2, pOther := mk(tid, "p1"), mk(tid, "p2"), mk("other", "po")
	for _, m := range []struct {
		tenant string
		pid    int
	}{{tid, p2}, {tid, p1}, {"other", pOther}} {
		if err := repo.AddProjectMember(m.tenant, m.pid, 42); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}

	// platform staff and tenant admin: all
	if all, ids := allowedProjectIDs(rbacCtx(common.RoleAdminUser), &middleware.TenantContext{TenantID: tid, UserID: 42}); !all || ids != nil {
		t.Fatalf("platform staff = (%v,%v), want (true,nil)", all, ids)
	}
	admin := &middleware.TenantContext{TenantID: tid, UserID: 1, RoleTenantID: tid, TenantRole: entity.TenantRoleAdmin}
	if all, _ := allowedProjectIDs(rbacCtx(1), admin); !all {
		t.Fatal("tenant admin must see all projects")
	}
	// dept_lead: only member projects of THIS tenant, sorted
	lead := &middleware.TenantContext{TenantID: tid, UserID: 42, RoleTenantID: tid, TenantRole: entity.TenantRoleDeptLead}
	all, ids := allowedProjectIDs(rbacCtx(1), lead)
	if all || !reflect.DeepEqual(ids, []int{p1, p2}) {
		t.Fatalf("dept_lead = (%v,%v), want (false,[%d %d])", all, ids, p1, p2)
	}
	// dept_lead whose role row belongs to another tenant: nothing
	stray := &middleware.TenantContext{TenantID: tid, UserID: 42, RoleTenantID: "other", TenantRole: entity.TenantRoleDeptLead}
	if all, ids := allowedProjectIDs(rbacCtx(1), stray); all || len(ids) != 0 {
		t.Fatalf("cross-tenant dept_lead = (%v,%v), want empty", all, ids)
	}
	// plain member: empty scope, not all
	member := &middleware.TenantContext{TenantID: tid, UserID: 42, RoleTenantID: tid}
	if all, ids := allowedProjectIDs(rbacCtx(1), member); all || len(ids) != 0 {
		t.Fatalf("plain member = (%v,%v), want empty", all, ids)
	}
}

// TestGetAllLogsV2_TenantAdminGetsUserView: a tenant admin (tenant_role=admin,
// global role 1) may read /logs/all but must not receive channel_name or any
// TierInternal key; platform staff receive the row untouched.
func TestGetAllLogsV2_TenantAdminGetsUserView(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	row := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		ModelName: "model-a", CreatedAt: 1700000000,
		Other: `{"cache_tokens":7,"model_ratio":2.5,"admin_info":{"route_attempts":[{"channel_id":7}]}}`,
	}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	serve := func(globalRole int, tenantRole string) map[string]interface{} {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set("role", globalRole)
			c.Set("tenant_context", &middleware.TenantContext{
				TenantID: ctx.TenantID, UserID: ctx.NormalUser.Id,
				RoleTenantID: ctx.TenantID, TenantRole: tenantRole, Roles: []string{},
			})
		})
		r.GET("/logs/all", GetAllLogsV2)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logs/all", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d body %s", w.Code, w.Body.String())
		}
		logs := ParseV2Response(t, w)["data"].(map[string]interface{})["logs"].([]interface{})
		if len(logs) != 1 {
			t.Fatalf("want 1 log, got %d", len(logs))
		}
		return logs[0].(map[string]interface{})
	}

	cust := serve(common.RoleCommonUser, entity.TenantRoleAdmin)
	if name, _ := cust["channel_name"].(string); name != "" {
		t.Errorf("tenant admin saw channel_name %q", name)
	}
	other, _ := cust["other"].(map[string]interface{})
	for _, k := range []string{"admin_info", "model_ratio"} {
		if _, leaked := other[k]; leaked {
			t.Errorf("tenant admin saw TierInternal key %q", k)
		}
	}
	if _, ok := other["cache_tokens"]; !ok {
		t.Error("user-tier key cache_tokens should survive sanitisation")
	}

	staff := serve(common.RoleAdminUser, "")
	if _, ok := staff["other"].(map[string]interface{})["admin_info"]; !ok {
		t.Error("platform staff must keep admin_info")
	}
}

// TestUpdateTenant_WalletAuthoritativeToggle: the root tenant update accepts
// wallet_authoritative and an explicit false turns it back off (pointer field,
// not the non-zero-only map pattern).
func TestUpdateTenant_WalletAuthoritativeToggle(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	put := func(v bool) {
		t.Helper()
		c, w := r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"wallet_authoritative": v})
		c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
		c.Set("user_id", ctx.AdminUser.Id)
		UpdateTenant(c)
		if w.Code != http.StatusOK {
			t.Fatalf("update(%v) = %d: %s", v, w.Code, w.Body.String())
		}
	}
	put(true)
	if !repo.TenantWalletAuthoritative(ctx.TenantID) {
		t.Fatal("wallet_authoritative=true was not persisted")
	}
	put(false)
	if repo.TenantWalletAuthoritative(ctx.TenantID) {
		t.Fatal("explicit false must switch wallet_authoritative back off")
	}
	if repo.TenantWalletAuthoritative("no-such-tenant") {
		t.Fatal("unknown tenant must fail closed")
	}
}

// tenantAdminRouter serves h as a customer tenant admin (tenant_role=admin,
// global role 1) or as platform staff (global role 10).
func tenantAdminRouter(ctx *V2TestContext, staff bool, method, path string, h gin.HandlerFunc) *gin.Engine {
	role := common.RoleCommonUser
	tr := entity.TenantRoleAdmin
	if staff {
		role = common.RoleAdminUser
		tr = ""
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", role)
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: ctx.TenantID, UserID: ctx.NormalUser.Id,
			RoleTenantID: ctx.TenantID, TenantRole: tr, Roles: []string{},
		})
	})
	r.Handle(method, path, h)
	return r
}

// TestPlatformOnlyV2Routes_TenantAdminDenied: requireTenantAdmin was widened to
// tenant_role=admin, so every caller that guards PLATFORM-only power must use
// isPlatformStaff. Redemption codes credit users.quota (free usage for tenants
// without wallet_authoritative); channels and the vendor ranking expose or
// control the supply chain. A customer admin must get 403 on all of them.
func TestPlatformOnlyV2Routes_TenantAdminDenied(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	routes := []struct {
		name, method, path string
		h                  gin.HandlerFunc
	}{
		{"redemption list", http.MethodGet, "/x", ListRedemptionsV2},
		{"redemption create", http.MethodPost, "/x", CreateRedemptionV2},
		{"redemption delete", http.MethodDelete, "/x", DeleteRedemptionV2},
		{"channel list", http.MethodGet, "/x", ListChannelsV2},
		{"channel create", http.MethodPost, "/x", CreateChannelV2},
		{"channel get", http.MethodGet, "/x", GetChannelV2},
		{"channel update", http.MethodPut, "/x", UpdateChannelV2},
		{"channel delete", http.MethodDelete, "/x", DeleteChannelV2},
		{"channel test", http.MethodGet, "/x", TestChannelV2},
		{"channel fetch upstream models", http.MethodGet, "/x", FetchUpstreamModelsV2},
		{"rankings by=vendor", http.MethodGet, "/x?by=vendor", GetTenantRankingsV2},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			path := rt.path
			r := tenantAdminRouter(ctx, false, rt.method, "/x", rt.h)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(rt.method, path, nil))
			if w.Code != http.StatusForbidden {
				t.Fatalf("tenant admin got %d, want 403: %s", w.Code, w.Body.String())
			}
		})
	}

	// Positive controls: platform staff pass the vendor gate; a tenant admin
	// keeps the non-supply-chain dimensions.
	w := httptest.NewRecorder()
	tenantAdminRouter(ctx, true, http.MethodGet, "/x", GetTenantRankingsV2).
		ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x?by=vendor", nil))
	if w.Code == http.StatusForbidden {
		t.Fatalf("platform staff must pass by=vendor, got 403")
	}
	w = httptest.NewRecorder()
	tenantAdminRouter(ctx, false, http.MethodGet, "/x", GetTenantRankingsV2).
		ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x?by=model", nil))
	if w.Code == http.StatusForbidden {
		t.Fatalf("tenant admin must keep by=model, got 403")
	}
}

// TestGetAllLogsV2_UpstreamRequestIDIgnoredForTenantAdmin: the vendor request
// id is TierInternal, so a customer admin's filter on it must be dropped
// (otherwise it is an existence oracle for upstream ids).
func TestGetAllLogsV2_UpstreamRequestIDIgnoredForTenantAdmin(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	row := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		ModelName: "model-a", CreatedAt: 1700000000, Other: `{"upstream_request_id":"up-123"}`,
	}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
	count := func(staff bool, q string) int {
		w := httptest.NewRecorder()
		tenantAdminRouter(ctx, staff, http.MethodGet, "/logs/all", GetAllLogsV2).
			ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logs/all?"+q, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		return len(ParseV2Response(t, w)["data"].(map[string]interface{})["logs"].([]interface{}))
	}
	if n := count(false, "upstream_request_id=nope"); n != 1 {
		t.Fatalf("tenant admin: filter must be ignored (want 1 row), got %d", n)
	}
	if n := count(true, "upstream_request_id=nope"); n != 0 {
		t.Fatalf("platform staff: filter must apply (want 0 rows), got %d", n)
	}
}

// TestToLogViews_ChannelNameBlankedForNonStaff pins the channel_name blanking
// directly: GetTenantLogsWithParams never fills ChannelName, so the handler-level
// test above cannot see it.
func TestToLogViews_ChannelNameBlankedForNonStaff(t *testing.T) {
	logs := []*repo.Log{{ChannelName: "openai-key3", Other: `{"admin_info":{"x":1}}`}}
	if got := toLogViews(logs, false)[0].ChannelName; got != "" {
		t.Fatalf("non-staff channel_name = %q, want empty", got)
	}
	if got := toLogViews(logs, true)[0].ChannelName; got != "openai-key3" {
		t.Fatalf("staff channel_name = %q, want kept", got)
	}
}

// requireTenantAdminAllowlist names the only handlers that may widen access to
// a customer's tenant admin by calling requireTenantAdmin. Everything else that
// guards supply-chain or platform-global power must call isPlatformStaff (or
// requirePlatformRoot). A new call site fails this gate until a human has
// decided it is customer-safe and added it here.
var requireTenantAdminAllowlist = map[string]bool{
	"GetAllLogsV2":               true, // /logs/all, projected through toLogViews
	"GetAllLogStatV2":            true, // /logs/stat/all
	"ListModelPerformanceV2":     true, // tenant model performance
	"projectAdminCtx":            true, // project write endpoints
	"GetTenantRankingsV2":        true, // rankings; by=vendor is gated by isPlatformStaff
	"allowedProjectIDs":          true, // data scope helper (v2_rbac.go)
	"GetBillingStatementV2":      true, // tenant monthly statement; rows are the tenant's own spend, no supply-chain fields
	"ExportLogsV2":               true, // scope=tenant CSV: the tenant's own rows, no channel / TierInternal columns
	"BatchCreateTokensV2":        true, // roster keys owned by the admin in their own tenant; never trusted-gateway keys
	"checkEnterpriseTokenFields": true, // trusted_identity_headers is a tenant-admin decision about the tenant's own gateway key
	"requireEmployeeRefAdmin":    true, // employee_ref is the tenant admin's own attribution roster for the tenant's keys
	"tenantAuditScope":           true, // tenant audit trail: own-tenant rows only, details redacted, no hash chain / channel data
	"tenantSelectionScope":       true, // tenant admin may only narrow the platform-granted model list
	"listTenantTokensV2":         true, // scope=tenant: own-tenant tokens, masked keys, no channel data
}

func TestRequireTenantAdminCallSites_Allowlisted(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob handler sources: %v (%d files)", err, len(files))
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "requireTenantAdmin" {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "requireTenantAdmin" {
					seen[fd.Name.Name] = true
					if !requireTenantAdminAllowlist[fd.Name.Name] {
						t.Errorf("%s calls requireTenantAdmin but is not in requireTenantAdminAllowlist: a customer tenant admin passes that gate — use isPlatformStaff unless the endpoint is customer-safe", fd.Name.Name)
					}
				}
				return true
			})
		}
	}
	// Anti-vacuous: the walk must actually find the known call sites.
	for name := range requireTenantAdminAllowlist {
		if !seen[name] {
			t.Errorf("allowlisted %s no longer calls requireTenantAdmin — drop it from the allowlist", name)
		}
	}
}
