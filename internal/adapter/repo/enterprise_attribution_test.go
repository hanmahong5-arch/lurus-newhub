package repo

// enterprise_attribution_test.go — HERMETIC (in-memory SQLite) coverage for
// the repo half of per-employee / per-department attribution (migration 045,
// docs/plans/enterprise-hub-2026-10-07.md section 2.1): the department
// resolver, the attribution columns on consume AND error rows, and the new
// LogQueryParams filters.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

func mustCreateCodedProject(t *testing.T, tenantID, name, code string) *entity.Project {
	t.Helper()
	p := &entity.Project{TenantId: tenantID, Name: name, ExternalCode: code}
	if err := DB.Create(p).Error; err != nil {
		t.Fatalf("create project %q: %v", name, err)
	}
	return p
}

func TestResolveProjectByDeptCode_CodeBeatsNameAndNameIsFallback(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ResetProjectDeptCache()

	// "R&D" is both the NAME of project byName and the CODE of project byCode:
	// the code must win.
	byName := mustCreateCodedProject(t, "ten-a", "R&D", "")
	byCode := mustCreateCodedProject(t, "ten-a", "Research", "R&D")
	onlyName := mustCreateCodedProject(t, "ten-a", "Finance", "")

	if id, ok := ResolveProjectByDeptCode("ten-a", "R&D"); !ok || id != byCode.Id {
		t.Errorf("resolve(R&D) = %d,%v; want the external_code match %d (name match was %d)", id, ok, byCode.Id, byName.Id)
	}
	if id, ok := ResolveProjectByDeptCode("ten-a", "Finance"); !ok || id != onlyName.Id {
		t.Errorf("resolve(Finance) = %d,%v; want the name fallback %d", id, ok, onlyName.Id)
	}
}

func TestResolveProjectByDeptCode_UnknownDoesNotCreate(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ResetProjectDeptCache()

	mustCreateCodedProject(t, "ten-a", "Existing", "E1")
	if id, ok := ResolveProjectByDeptCode("ten-a", "NoSuchDept"); ok || id != 0 {
		t.Fatalf("resolve(unknown) = %d,%v; want 0,false", id, ok)
	}
	var n int64
	if err := DB.Model(&entity.Project{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("project count = %d after an unknown dept; want 1 — a header must never create a project", n)
	}
}

func TestResolveProjectByDeptCode_TenantIsolation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ResetProjectDeptCache()

	other := mustCreateCodedProject(t, "ten-b", "Sales", "S1")
	// Neither the same name nor the same code of ANOTHER tenant may match.
	if id, ok := ResolveProjectByDeptCode("ten-a", "Sales"); ok {
		t.Errorf("tenant ten-a matched ten-b's project by name (id %d)", id)
	}
	if id, ok := ResolveProjectByDeptCode("ten-a", "S1"); ok {
		t.Errorf("tenant ten-a matched ten-b's project by code (id %d)", id)
	}
	// Positive control, and it also warms the cache for ten-b: ten-a's miss
	// must not have poisoned ten-b's key.
	if id, ok := ResolveProjectByDeptCode("ten-b", "S1"); !ok || id != other.Id {
		t.Errorf("ten-b resolve(S1) = %d,%v; want %d", id, ok, other.Id)
	}
	if _, ok := ResolveProjectByDeptCode("ten-a", "S1"); ok {
		t.Error("ten-a matched S1 from cache after ten-b warmed its own entry")
	}
	if _, ok := ResolveProjectByDeptCode("", "S1"); ok {
		t.Error("empty tenant must not match")
	}
}

func TestResolveProjectByDeptCode_SoftDeletedAndCacheTTL(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ResetProjectDeptCache()

	now := time.Now()
	deptCacheNow = func() time.Time { return now }
	defer func() { deptCacheNow = time.Now }()

	p := mustCreateCodedProject(t, "ten-a", "Ops", "OPS")
	if id, ok := ResolveProjectByDeptCode("ten-a", "OPS"); !ok || id != p.Id {
		t.Fatalf("initial resolve = %d,%v", id, ok)
	}
	if err := DB.Delete(p).Error; err != nil {
		t.Fatal(err)
	}
	// Inside the TTL the cached mapping is still served...
	if _, ok := ResolveProjectByDeptCode("ten-a", "OPS"); !ok {
		t.Error("cache entry should survive inside the TTL")
	}
	// ...and past it the soft-deleted project no longer resolves.
	now = now.Add(deptCacheTTL + time.Second)
	if id, ok := ResolveProjectByDeptCode("ten-a", "OPS"); ok {
		t.Errorf("soft-deleted project still resolves after TTL (id %d)", id)
	}
}

func TestRecordErrorLog_WritesProjectAndEmployee(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "attr-err-user", "attr-err@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyProjectId, 77)
	common.SetContextKey(c, constant.ContextKeyEmployeeRef, "emp-0042")

	RecordErrorLog(c, u.Id, 0, "test-model", "k", "upstream 500", tok.Id, 1, false, "default", nil)

	var lg Log
	if err := LOG_DB.Where("user_id = ? AND type = ?", u.Id, LogTypeError).First(&lg).Error; err != nil {
		t.Fatalf("error log not written: %v", err)
	}
	if lg.ProjectId != 77 || lg.EmployeeRef != "emp-0042" {
		t.Errorf("error row project_id=%d employee_ref=%q; want 77 / emp-0042", lg.ProjectId, lg.EmployeeRef)
	}
}

func TestRecordConsumeLog_WritesEmployeeRef(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	defer func() { common.LogConsumeEnabled = prev }()

	u := seedUser(t, "attr-con-user", "attr-con@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	RecordConsumeLog(c, u.Id, RecordConsumeLogParams{ModelName: "test-model", Quota: 5, ProjectId: 9, EmployeeRef: "emp-7"})

	var lg Log
	if err := LOG_DB.Where("user_id = ? AND type = ?", u.Id, LogTypeConsume).First(&lg).Error; err != nil {
		t.Fatalf("consume log not written: %v", err)
	}
	if lg.ProjectId != 9 || lg.EmployeeRef != "emp-7" {
		t.Errorf("consume row project_id=%d employee_ref=%q; want 9 / emp-7", lg.ProjectId, lg.EmployeeRef)
	}
}

func seedAttrLog(t *testing.T, tenantID string, projectID int, emp string) {
	t.Helper()
	row := &Log{UserId: 1, TenantId: tenantID, Type: LogTypeConsume, CreatedAt: common.GetTimestamp(),
		ModelName: "test-model-2", Quota: 1, ProjectId: projectID, EmployeeRef: emp}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

func TestLogQueryParams_ProjectIDsAndEmployeeRefFilters(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedAttrLog(t, "ten-a", 1, "alice")
	seedAttrLog(t, "ten-a", 1, "bob")
	seedAttrLog(t, "ten-a", 2, "alice")
	seedAttrLog(t, "ten-a", 3, "carol")
	seedAttrLog(t, "ten-b", 1, "alice") // other tenant, same project id + employee

	tenantTotal := func(p LogQueryParams) int64 {
		p.Limit = 50
		_, total, err := GetTenantLogsWithParams(ForTenant("ten-a"), &p)
		if err != nil {
			t.Fatalf("GetTenantLogsWithParams: %v", err)
		}
		return total
	}
	userTotal := func(p LogQueryParams) int64 {
		p.Limit = 50
		_, total, err := GetUserLogsWithParams(ForTenant("ten-a"), &p)
		if err != nil {
			t.Fatalf("GetUserLogsWithParams: %v", err)
		}
		return total
	}

	cases := []struct {
		name string
		p    LogQueryParams
		want int64
	}{
		{"no attribution filter", LogQueryParams{}, 4},
		{"ProjectIDs {1}", LogQueryParams{ProjectIDs: []int{1}}, 2},
		{"ProjectIDs {1,3}", LogQueryParams{ProjectIDs: []int{1, 3}}, 3},
		{"ProjectIDs empty non-nil matches nothing", LogQueryParams{ProjectIDs: []int{}}, 0},
		{"EmployeeRef exact", LogQueryParams{EmployeeRef: "alice"}, 2},
		{"EmployeeRef is not a prefix match", LogQueryParams{EmployeeRef: "ali"}, 0},
		{"ProjectIDs AND EmployeeRef", LogQueryParams{ProjectIDs: []int{1}, EmployeeRef: "alice"}, 1},
		{"ProjectIDs AND ProjectID", LogQueryParams{ProjectIDs: []int{1, 2}, ProjectID: 2}, 1},
	}
	for _, tc := range cases {
		if got := tenantTotal(tc.p); got != tc.want {
			t.Errorf("tenant scope %q: total = %d, want %d", tc.name, got, tc.want)
		}
		if got := userTotal(tc.p); got != tc.want {
			t.Errorf("user scope %q: total = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// employee_ref is personal data (usually an email): the erasure cascade must
// clear it together with username/ip.
func TestAnonymizeLogsBatch_ClearsEmployeeRef(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	row := &Log{UserId: 77, TenantId: "ten-a", Type: LogTypeConsume, CreatedAt: common.GetTimestamp(),
		Username: "payer", ModelName: "test-model", Quota: 1, ProjectId: 4, EmployeeRef: "alice@corp.example"}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
	ids, err := AnonymizeLogsBatch(context.Background(), 77, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("AnonymizeLogsBatch = %v, %v; want 1 id", ids, err)
	}
	var got Log
	if err := LOG_DB.First(&got, row.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.EmployeeRef != "" {
		t.Errorf("employee_ref after erasure = %q, want empty", got.EmployeeRef)
	}
	if got.ProjectId != 4 {
		t.Errorf("project_id = %d, want 4 retained (billing attribution)", got.ProjectId)
	}
}

// A cached miss must expire quickly so a newly created external_code starts
// attributing within seconds (positive entries keep the longer TTL).
func TestResolveProjectByDeptCode_NegativeResultExpiresQuickly(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ResetProjectDeptCache()
	now := time.Now()
	deptCacheNow = func() time.Time { return now }
	defer func() { deptCacheNow = time.Now; ResetProjectDeptCache() }()

	if _, ok := ResolveProjectByDeptCode("ten-a", "NEW1"); ok {
		t.Fatal("unexpected hit before the project exists")
	}
	p := mustCreateCodedProject(t, "ten-a", "Later", "NEW1")
	if _, ok := ResolveProjectByDeptCode("ten-a", "NEW1"); ok {
		t.Fatal("miss should still be served from cache inside the negative TTL")
	}
	now = now.Add(deptNegativeTTL + time.Second)
	if id, ok := ResolveProjectByDeptCode("ten-a", "NEW1"); !ok || id != p.Id {
		t.Errorf("after negative TTL resolve = %d,%v; want %d", id, ok, p.Id)
	}
	// A hit is cached for the longer TTL: deleting the row is not seen at +10s.
	DB.Delete(p)
	now = now.Add(10 * time.Second)
	if _, ok := ResolveProjectByDeptCode("ten-a", "NEW1"); !ok {
		t.Error("positive entry expired at the negative TTL; want it kept for deptCacheTTL")
	}
}

// Token.Update must persist the trust flag and employee_ref: a silently
// dropped DB write would leave the cache (refreshed after Update) and the DB
// disagreeing on whether a key may name employees.
func TestTokenUpdate_PersistsTrustAndEmployeeRef(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "attr-tok-user", "attr-tok@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := &Token{UserId: u.Id, TenantId: "default", Name: "t1", Key: "attrkey0000000000000000000000000000000000000000", Status: 1}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	tok.TrustedIdentityHeaders = true
	tok.EmployeeRef = "emp-9"
	if err := tok.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var got Token
	if err := DB.First(&got, tok.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !got.TrustedIdentityHeaders || got.EmployeeRef != "emp-9" {
		t.Errorf("persisted trusted=%v employee_ref=%q; want true / emp-9", got.TrustedIdentityHeaders, got.EmployeeRef)
	}
}
