package handler

// v2_enterprise_scope_test.go — lane B1: department-lead data scope on the
// log list / stat / export / rankings routes and the tenant monthly
// statement. One hermetic SQLite router; the caller's identity is picked per
// request with X-Test-Who (member | lead | admin | lead0 | platform).

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var entScopeDBCounter atomic.Int64

const (
	entMemberID = 11
	entLeadID   = 12
	entAdminID  = 13
	entLead0ID  = 14 // dept_lead with no project membership
)

type entScopeCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	tenantID string
	slug     string
	p1, p2   int
}

func setupEntScope(t *testing.T) *entScopeCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := entScopeDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:entscope%d?mode=memory&cache=shared", n)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &repo.Log{}, &repo.Option{}, &entity.Project{}, &entity.ProjectMember{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}
	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	repo.DB, repo.LOG_DB = db, db
	repo.InitCol()
	common.UsingSQLite, common.UsingPostgreSQL = true, false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLog
		common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		resetRankingsCacheForTest()
	})
	resetRankingsCacheForTest()

	c := &entScopeCtx{db: db, tenantID: fmt.Sprintf("tenant-ent-%d", n), slug: fmt.Sprintf("ent-%d", n)}
	now := time.Now()
	if err := db.Create(&repo.Tenant{Id: c.tenantID, Name: "Ent", Slug: c.slug, Status: repo.TenantStatusEnabled,
		IDPOrgID: "org_" + c.slug, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	pa := &entity.Project{TenantId: c.tenantID, Name: "dept-a"}
	pb := &entity.Project{TenantId: c.tenantID, Name: "dept-b"}
	for _, p := range []*entity.Project{pa, pb} {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	c.p1, c.p2 = pa.Id, pb.Id
	if err := repo.AddProjectMember(c.tenantID, c.p1, entLeadID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	mockAuth := func(ctx *gin.Context) {
		tc := &middleware.TenantContext{TenantID: c.tenantID, RoleTenantID: c.tenantID}
		role := common.RoleCommonUser
		switch ctx.GetHeader("X-Test-Who") {
		case "lead":
			tc.UserID, tc.TenantRole = entLeadID, entity.TenantRoleDeptLead
		case "lead0":
			tc.UserID, tc.TenantRole = entLead0ID, entity.TenantRoleDeptLead
		case "admin":
			tc.UserID, tc.TenantRole = entAdminID, entity.TenantRoleAdmin
		case "platform":
			tc.UserID, role = 99, common.RoleAdminUser
		default:
			tc.UserID = entMemberID
		}
		ctx.Set("role", role)
		ctx.Set("tenant_context", tc)
		ctx.Next()
	}
	r := gin.New()
	g := r.Group("/api/v2/:tenant_slug", mockAuth)
	g.GET("/logs", GetLogsV2)
	g.GET("/logs/stat", GetLogStatV2)
	g.GET("/logs/export", ExportLogsV2)
	g.GET("/analytics/rankings", GetTenantRankingsV2)
	g.GET("/billing/statement", GetBillingStatementV2)
	c.router = r
	return c
}

type entLogSeed struct {
	user, project int
	employee      string
	model         string
	quota         int
	charged       int64
	priced        int64
	at            int64
	tenant        string
	token         int
	tokenName     string
	other         string
}

func (c *entScopeCtx) seed(t *testing.T, s entLogSeed) {
	t.Helper()
	if s.tenant == "" {
		s.tenant = c.tenantID
	}
	if s.model == "" {
		s.model = "m-default"
	}
	if s.at == 0 {
		s.at = time.Now().Unix() - 60
	}
	l := &repo.Log{UserId: s.user, TenantId: s.tenant, Type: repo.LogTypeConsume, ModelName: s.model,
		Quota: s.quota, PromptTokens: 10, CompletionTokens: 5, ProjectId: s.project, EmployeeRef: s.employee,
		ChargedCNY4: s.charged, PricedCNY4: s.priced, CreatedAt: s.at, TokenId: s.token, TokenName: s.tokenName,
		Other: s.other}
	if err := c.db.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

// seedStandard: member user logs in p1 and unassigned, another user's logs in
// p1 and p2, and a foreign-tenant row in a colliding project id.
func (c *entScopeCtx) seedStandard(t *testing.T) {
	c.seed(t, entLogSeed{user: entMemberID, project: c.p1, employee: "alice", model: "mA", quota: 100})
	c.seed(t, entLogSeed{user: entMemberID, project: 0, employee: "alice", model: "mU", quota: 10})
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", model: "mA", quota: 200})
	c.seed(t, entLogSeed{user: 22, project: c.p2, employee: "carol", model: "mB", quota: 400})
	c.seed(t, entLogSeed{user: 23, project: c.p1, tenant: "tenant-foreign", model: "mF", quota: 800})
}

func (c *entScopeCtx) get(who, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+c.slug+path, nil)
	req.Header.Set("X-Test-Who", who)
	w := httptest.NewRecorder()
	c.router.ServeHTTP(w, req)
	return w
}

func entJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return m
}

func entData(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	return entJSON(t, w)["data"].(map[string]interface{})
}

// ---- logs list ------------------------------------------------------------

func TestEntScope_LogsList_ThreeIdentities(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)

	if got := entData(t, c.get("member", "/logs"))["total"].(float64); got != 2 {
		t.Errorf("member total = %v, want 2 (own rows only, unchanged)", got)
	}
	// dept_lead: the whole p1 department (two users), none of p2/unassigned/foreign.
	if got := entData(t, c.get("lead", "/logs"))["total"].(float64); got != 2 {
		t.Errorf("dept_lead total = %v, want 2 (p1 rows of every user)", got)
	}
	if got := entData(t, c.get("admin", "/logs"))["total"].(float64); got != 0 {
		t.Errorf("admin /logs total = %v, want 0 (own rows only; tenant view is /logs/all)", got)
	}
}

func TestEntScope_LogsList_DeptLeadForeignProject404(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)
	if w := c.get("lead", fmt.Sprintf("/logs?project_id=%d", c.p2)); w.Code != http.StatusNotFound {
		t.Fatalf("lead querying a foreign project: status = %d, want 404 (not 403: no probing)", w.Code)
	}
	if got := entData(t, c.get("lead", fmt.Sprintf("/logs?project_id=%d", c.p1)))["total"].(float64); got != 2 {
		t.Errorf("lead own project filter total = %v, want 2", got)
	}
	// A member (not a lead) keeps the old behaviour: the filter is just a filter.
	if w := c.get("member", fmt.Sprintf("/logs?project_id=%d", c.p2)); w.Code != http.StatusOK {
		t.Errorf("member project filter status = %d, want 200", w.Code)
	}
}

func TestEntScope_LogsList_DeptLeadWithoutProjectsSeesNothing(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)
	w := c.get("lead0", "/logs")
	if got := entData(t, w)["total"].(float64); got != 0 {
		t.Fatalf("lead with no project total = %v, want 0 (must not fall back to tenant-wide)", got)
	}
}

// ---- stat -----------------------------------------------------------------

func TestEntScope_Stat(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)
	d := entData(t, c.get("lead", "/logs/stat"))
	if d["total_requests"].(float64) != 2 || d["total_quota"].(float64) != 300 {
		t.Errorf("lead stat = %v, want 2 requests / 300 quota (p1, both users)", d)
	}
	if w := c.get("lead", fmt.Sprintf("/logs/stat?project_id=%d", c.p2)); w.Code != http.StatusNotFound {
		t.Errorf("lead stat foreign project status = %d, want 404", w.Code)
	}
	m := entData(t, c.get("member", "/logs/stat"))
	if m["total_requests"].(float64) != 2 || m["total_quota"].(float64) != 110 {
		t.Errorf("member stat = %v, want own 2 rows / 110", m)
	}
	if z := entData(t, c.get("lead0", "/logs/stat")); z["total_requests"].(float64) != 0 {
		t.Errorf("lead0 stat = %v, want 0", z)
	}
}

// ---- export ---------------------------------------------------------------

func entCSV(t *testing.T, w *httptest.ResponseRecorder) [][]string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", w.Code, w.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return rows
}

func entCol(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("csv header %v lacks column %q", header, name)
	return -1
}

func TestEntScope_Export_Scopes(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)

	lead := entCSV(t, c.get("lead", "/logs/export"))
	if len(lead)-1 != 2 {
		t.Errorf("lead export rows = %d, want 2 (p1 of every user)", len(lead)-1)
	}
	if w := c.get("lead", fmt.Sprintf("/logs/export?project_id=%d", c.p2)); w.Code != http.StatusNotFound {
		t.Errorf("lead export foreign project status = %d, want 404", w.Code)
	}
	if w := c.get("lead", "/logs/export?scope=tenant"); w.Code != http.StatusForbidden {
		t.Errorf("lead scope=tenant status = %d, want 403", w.Code)
	}
	if w := c.get("member", "/logs/export?scope=tenant"); w.Code != http.StatusForbidden {
		t.Errorf("member scope=tenant status = %d, want 403", w.Code)
	}
	if rows := entCSV(t, c.get("member", "/logs/export")); len(rows)-1 != 2 {
		t.Errorf("member export rows = %d, want own 2", len(rows)-1)
	}
	if rows := entCSV(t, c.get("admin", "/logs/export")); len(rows)-1 != 0 {
		t.Errorf("admin default export rows = %d, want own 0", len(rows)-1)
	}
	all := entCSV(t, c.get("admin", "/logs/export?scope=tenant"))
	if len(all)-1 != 4 {
		t.Errorf("admin tenant export rows = %d, want 4 (foreign tenant excluded)", len(all)-1)
	}
}

func TestEntScope_Export_Columns(t *testing.T) {
	c := setupEntScope(t)
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", token: 77, tokenName: "k", quota: 5,
		other: `{"request_id":"req-abc","source_product":"x"}`})
	rows := entCSV(t, c.get("admin", "/logs/export?scope=tenant"))
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	h, r := rows[0], rows[1]
	for col, want := range map[string]string{
		"employee_ref": "bob", "project_id": fmt.Sprint(c.p1), "token_id": "77", "request_id": "req-abc",
	} {
		if got := r[entCol(t, h, col)]; got != want {
			t.Errorf("column %s = %q, want %q", col, got, want)
		}
	}
}

// ---- rankings -------------------------------------------------------------

func TestEntScope_Rankings(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)
	names := func(w *httptest.ResponseRecorder) map[string]bool {
		out := map[string]bool{}
		for _, r := range entData(t, w)["rows"].([]interface{}) {
			out[r.(map[string]interface{})["name"].(string)] = true
		}
		return out
	}
	// Warm the admin entry first: the lead must not be served the wide cache.
	admin := names(c.get("admin", "/analytics/rankings?by=model"))
	if !admin["mA"] || !admin["mB"] || !admin["mU"] || admin["mF"] {
		t.Errorf("admin rankings = %v, want whole tenant", admin)
	}
	lead := names(c.get("lead", "/analytics/rankings?by=model"))
	if !lead["mA"] || lead["mB"] || lead["mU"] || lead["mF"] || len(lead) != 1 {
		t.Errorf("lead rankings = %v, want only mA (p1)", lead)
	}
	if w := c.get("member", "/analytics/rankings"); w.Code != http.StatusForbidden {
		t.Errorf("member rankings status = %d, want 403", w.Code)
	}
	if w := c.get("lead", "/analytics/rankings?by=vendor"); w.Code != http.StatusForbidden {
		t.Errorf("lead by=vendor status = %d, want 403 (platform-only)", w.Code)
	}
	if w := c.get("admin", "/analytics/rankings?by=vendor"); w.Code != http.StatusForbidden {
		t.Errorf("tenant admin by=vendor status = %d, want 403 (platform-only)", w.Code)
	}
	if rows := entData(t, c.get("lead0", "/analytics/rankings?by=model"))["rows"].([]interface{}); len(rows) != 0 {
		t.Errorf("lead0 rankings rows = %d, want 0", len(rows))
	}
}

// ---- monthly statement ----------------------------------------------------

func entUnix(t *testing.T, tz string, y int, m time.Month, d, h, min int) int64 {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("tz: %v", err)
	}
	return time.Date(y, m, d, h, min, 0, 0, loc).Unix()
}

func (c *entScopeCtx) seedStatement(t *testing.T) {
	sh := "Asia/Shanghai"
	// Wallet-charged rows (charged wins), pool rows (priced), and a pre-042
	// row with neither (priced from quota at today's rate).
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", token: 1, tokenName: "k1", quota: 500, charged: 30000, priced: 30000, at: entUnix(t, sh, 2026, 10, 5, 12, 0)})
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", token: 1, tokenName: "k1", quota: 700, priced: 12345, at: entUnix(t, sh, 2026, 10, 6, 12, 0)})
	c.seed(t, entLogSeed{user: 22, project: c.p2, employee: "carol", token: 2, tokenName: "k2", quota: 900, charged: 777, priced: 777, at: entUnix(t, sh, 2026, 10, 7, 12, 0)})
	c.seed(t, entLogSeed{user: 22, project: 0, employee: "", token: 3, tokenName: "k3", quota: 1000, at: entUnix(t, sh, 2026, 10, 8, 12, 0)})
	// Month edge: 00:30 Beijing on Oct 1 is Sep 30 UTC — belongs to October.
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", token: 1, tokenName: "k1", quota: 11, charged: 100, priced: 100, at: entUnix(t, sh, 2026, 10, 1, 0, 30)})
	// ... and 23:30 Beijing on Sep 30 belongs to September.
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", token: 1, tokenName: "k1", quota: 13, charged: 200, priced: 200, at: entUnix(t, sh, 2026, 9, 30, 23, 30)})
	// Not billable: a settlement-failed consume row.
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", quota: 99999, priced: 99999, at: entUnix(t, sh, 2026, 10, 9, 12, 0), other: `{"settlement":"failed"}`})
	// Other tenant.
	c.seed(t, entLogSeed{user: 23, project: c.p1, tenant: "tenant-foreign", quota: 5, charged: 5, priced: 5, at: entUnix(t, sh, 2026, 10, 9, 12, 0)})
}

type stmtRow struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Requests    int64  `json:"requests"`
	Quota       int64  `json:"quota"`
	ChargedCNY4 int64  `json:"charged_cny4"`
	PricedCNY4  int64  `json:"priced_cny4"`
}

func (c *entScopeCtx) statement(t *testing.T, who, query string) (rows []stmtRow, totals stmtRow) {
	t.Helper()
	d := entData(t, c.get(who, "/billing/statement?"+query))
	b, _ := json.Marshal(d["rows"])
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("rows: %v", err)
	}
	b, _ = json.Marshal(d["totals"])
	if err := json.Unmarshal(b, &totals); err != nil {
		t.Fatalf("totals: %v", err)
	}
	return rows, totals
}

func sumRows(rows []stmtRow) (s stmtRow) {
	for _, r := range rows {
		s.Requests += r.Requests
		s.Quota += r.Quota
		s.ChargedCNY4 += r.ChargedCNY4
		s.PricedCNY4 += r.PricedCNY4
	}
	return s
}

func TestEntScope_Statement_RowsSumToTotals(t *testing.T) {
	c := setupEntScope(t)
	c.seedStatement(t)
	for _, gb := range []string{"project", "employee", "token"} {
		rows, totals := c.statement(t, "admin", "month=2026-10&group_by="+gb)
		if len(rows) == 0 {
			t.Fatalf("group_by=%s: no rows", gb)
		}
		got := sumRows(rows)
		if got != (stmtRow{Requests: totals.Requests, Quota: totals.Quota, ChargedCNY4: totals.ChargedCNY4, PricedCNY4: totals.PricedCNY4}) {
			t.Errorf("group_by=%s: sum(rows)=%+v != totals=%+v", gb, got, totals)
		}
		// 5 billable October rows; failed settlement and foreign tenant excluded.
		if totals.Requests != 5 {
			t.Errorf("group_by=%s: totals.requests = %d, want 5", gb, totals.Requests)
		}
		if totals.ChargedCNY4 != 30000+777+100 {
			t.Errorf("group_by=%s: totals.charged_cny4 = %d, want %d (wallet debits)", gb, totals.ChargedCNY4, 30000+777+100)
		}
	}
}

func TestEntScope_Statement_PricedOnlyForUnchargedRows(t *testing.T) {
	c := setupEntScope(t)
	c.seedStatement(t)
	_, totals := c.statement(t, "admin", "month=2026-10&group_by=project")
	// Priced share: the 12345 pool row + the quota-priced pre-042 row (>0);
	// the charged rows' priced_cny4 must NOT be added on top of charged.
	if totals.PricedCNY4 < 12345 || totals.PricedCNY4 >= 12345+30000 {
		t.Errorf("totals.priced_cny4 = %d: want 12345 plus only the unpriced row's quota price", totals.PricedCNY4)
	}
}

func TestEntScope_Statement_TimezoneMonthBoundary(t *testing.T) {
	c := setupEntScope(t)
	c.seedStatement(t)
	_, oct := c.statement(t, "admin", "month=2026-10&tz=Asia/Shanghai&group_by=project")
	_, sep := c.statement(t, "admin", "month=2026-09&tz=Asia/Shanghai&group_by=project")
	if sep.Requests != 1 || sep.Quota != 13 {
		t.Errorf("September (Shanghai) = %+v, want only the 23:30 Sep-30 row", sep)
	}
	if oct.Quota != 500+700+900+1000+11 {
		t.Errorf("October (Shanghai) quota = %d, want %d (00:30 Oct-1 row included)", oct.Quota, 500+700+900+1000+11)
	}
	// Same instants in UTC: the 00:30 Beijing row is September 30.
	_, octUTC := c.statement(t, "admin", "month=2026-10&tz=UTC&group_by=project")
	if octUTC.Quota != oct.Quota-11 {
		t.Errorf("October (UTC) quota = %d, want %d (edge row falls in September)", octUTC.Quota, oct.Quota-11)
	}
	// Default tz is Asia/Shanghai.
	_, def := c.statement(t, "admin", "month=2026-10")
	if def.Quota != oct.Quota {
		t.Errorf("default tz totals = %d, want Shanghai %d", def.Quota, oct.Quota)
	}
}

func TestEntScope_Statement_DeptLeadScope(t *testing.T) {
	c := setupEntScope(t)
	c.seedStatement(t)
	rows, totals := c.statement(t, "lead", "month=2026-10&group_by=project")
	if len(rows) != 1 || rows[0].Key != fmt.Sprint(c.p1) {
		t.Fatalf("lead rows = %+v, want only project %d (0-bucket and other departments hidden)", rows, c.p1)
	}
	if totals.Quota != 500+700+11 || totals.Requests != 3 {
		t.Errorf("lead totals = %+v, want only p1 rows", totals)
	}
	if rows, _ := c.statement(t, "lead", "month=2026-10&group_by=employee"); len(rows) != 1 || rows[0].Key != "bob" {
		t.Errorf("lead employee rows = %+v, want only bob", rows)
	}
	if rows, totals := c.statement(t, "lead0", "month=2026-10&group_by=token"); len(rows) != 0 || totals.Requests != 0 {
		t.Errorf("lead0 = %+v / %+v, want empty", rows, totals)
	}
	// Admin sees the unassigned bucket.
	rows, _ = c.statement(t, "admin", "month=2026-10&group_by=project")
	found := false
	for _, r := range rows {
		if r.Key == "0" {
			found = true
		}
	}
	if !found {
		t.Errorf("admin project rows %+v lack the unassigned bucket", rows)
	}
}

func TestEntScope_Statement_LabelsAndAccess(t *testing.T) {
	c := setupEntScope(t)
	c.seedStatement(t)
	rows, _ := c.statement(t, "admin", "month=2026-10&group_by=project")
	for _, r := range rows {
		if r.Key == fmt.Sprint(c.p1) && r.Label != "dept-a" {
			t.Errorf("project label = %q, want dept-a", r.Label)
		}
	}
	tok, _ := c.statement(t, "admin", "month=2026-10&group_by=token")
	for _, r := range tok {
		if r.Key == "1" && r.Label != "k1" {
			t.Errorf("token label = %q, want k1", r.Label)
		}
	}
	if w := c.get("member", "/billing/statement?month=2026-10"); w.Code != http.StatusForbidden {
		t.Errorf("member status = %d, want 403", w.Code)
	}
	for _, q := range []string{"month=2026-13", "month=oct", "month=2026-10&tz=Mars/Base", "month=2026-10&group_by=model"} {
		if w := c.get("admin", "/billing/statement?"+q); w.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want 400", q, w.Code)
		}
	}
}

func TestEntScope_Statement_CSV(t *testing.T) {
	c := setupEntScope(t)
	c.seedStatement(t)
	w := c.get("admin", "/billing/statement?month=2026-10&group_by=employee&format=csv")
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
	rows := entCSV(t, w)
	want := []string{"key", "label", "requests", "quota", "charged_cny4", "priced_cny4"}
	if strings.Join(rows[0], ",") != strings.Join(want, ",") {
		t.Fatalf("csv header = %v, want %v", rows[0], want)
	}
	last := rows[len(rows)-1]
	if last[0] != "*" {
		t.Errorf("last csv row = %v, want totals row keyed *", last)
	}
}

// ---- cross-department leak guards ----------------------------------------

// The stat route runs three separate queries (window totals, rpm/tpm, the
// by_product breakdown); each needs its own project filter. Recent rows of a
// second department sharing source_product and model make a missing filter on
// any one of them visible.
func TestEntScope_Stat_NoCrossDeptLeak(t *testing.T) {
	c := setupEntScope(t)
	recent := time.Now().Unix() - 5
	c.seed(t, entLogSeed{user: 21, project: c.p1, employee: "bob", model: "mX", quota: 100, at: recent})
	c.seed(t, entLogSeed{user: 22, project: c.p2, employee: "carol", model: "mX", quota: 1000, at: recent})
	c.seed(t, entLogSeed{user: 22, project: c.p2, employee: "carol", model: "mX", quota: 2000, at: recent})

	d := entData(t, c.get("lead", "/logs/stat"))
	if d["total_requests"].(float64) != 1 || d["total_quota"].(float64) != 100 {
		t.Errorf("lead window totals = %v, want 1 request / 100 quota", d)
	}
	if d["rpm"].(float64) != 1 {
		t.Errorf("lead rpm = %v, want 1 (own department only)", d["rpm"])
	}
	if d["tpm"].(float64) != 15 {
		t.Errorf("lead tpm = %v, want 15 (one row of 10+5 tokens)", d["tpm"])
	}
	var reqs, quota float64
	for _, r := range d["by_product"].([]interface{}) {
		row := r.(map[string]interface{})
		reqs += row["total_requests"].(float64)
		quota += row["total_quota"].(float64)
	}
	if reqs != 1 || quota != 100 {
		t.Errorf("lead by_product sums = %v requests / %v quota, want 1 / 100", reqs, quota)
	}
}

// Two departments using the same model: the lead's chart series must carry
// only their own department's volume.
func TestEntScope_Rankings_SeriesNoCrossDeptLeak(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)
	c.seed(t, entLogSeed{user: 22, project: c.p2, employee: "carol", model: "mA", quota: 5000})

	d := entData(t, c.get("lead", "/analytics/rankings?by=model"))
	var quota, reqs float64
	for _, p := range d["series"].([]interface{}) {
		pt := p.(map[string]interface{})
		if pt["name"] == "mA" {
			quota += pt["quota"].(float64)
			reqs += pt["requests"].(float64)
		}
	}
	if quota != 300 || reqs != 2 {
		t.Errorf("lead mA series sums = %v quota / %v requests, want 300 / 2 (p1 only)", quota, reqs)
	}
}

// When the membership lookup fails a dept_lead must see nothing, never fall
// back to the tenant-wide view.
func TestEntScope_DeptLead_MembershipLookupFailureFailsClosed(t *testing.T) {
	c := setupEntScope(t)
	c.seedStandard(t)
	c.seedStatement(t)
	if err := c.db.Migrator().DropTable(&entity.ProjectMember{}); err != nil {
		t.Fatalf("drop project_members: %v", err)
	}

	rowsOf := func(path string) float64 {
		w := c.get("lead", path)
		if w.Code != http.StatusOK {
			return 0 // refusing is also closed
		}
		d := entJSON(t, w)["data"].(map[string]interface{})
		switch {
		case d["total"] != nil:
			return d["total"].(float64)
		case d["total_requests"] != nil:
			return d["total_requests"].(float64)
		case d["totals"] != nil:
			return d["totals"].(map[string]interface{})["requests"].(float64)
		}
		return -1
	}
	for _, path := range []string{"/logs", "/logs/stat", "/billing/statement?month=2026-10"} {
		if got := rowsOf(path); got != 0 {
			t.Errorf("%s: lead saw %v rows with failing membership lookup, want 0", path, got)
		}
	}
	if w := c.get("lead", "/logs/export"); w.Code == http.StatusOK {
		if rows := entCSV(t, w); len(rows)-1 != 0 {
			t.Errorf("export: lead got %d rows with failing membership lookup, want 0", len(rows)-1)
		}
	}
}
