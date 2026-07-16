package repo

// log_tenant_scope_test.go — cross-tenant isolation guards for the logs table.
//
// The logs table is deliberately excluded from TenantPlugin auto-scoping, so
// isolation is enforced structurally in log.go: every exported query is
// either principal-scoped (user_id / token_id / token key) or requires an
// explicit TenantScope. These tests seed two tenants and assert, function by
// function, that tenant A's scope never sees tenant B's rows, that
// AllTenantsForAdmin still spans both (platform-admin surfaces), that the
// empty tenant id fails closed, and that the write paths stamp the owning
// user's tenant when the gin context carries none (plain /v1 relay path).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

type logScopeFixture struct {
	tenantA, tenantB *Tenant
	userA, userB     *User
	tokenA, tokenB   *Token
}

// seedLogScopeFixture seeds two tenants, one user + token each, and three log
// rows per side (consume / error / topup). Both consume rows carry the SAME
// denormalized username ("shared-name") so the username filter alone provably
// does not isolate tenants — only the scope does.
func seedLogScopeFixture(t *testing.T) logScopeFixture {
	t.Helper()

	f := logScopeFixture{
		tenantA: seedTenant(t, "scope-tenant-a", "scope-a", "Scope Tenant A"),
		tenantB: seedTenant(t, "scope-tenant-b", "scope-b", "Scope Tenant B"),
	}
	f.userA = seedUser(t, "scope-user-a", "scope-a@test.local", common.RoleCommonUser, common.UserStatusEnabled, f.tenantA.Id)
	f.userB = seedUser(t, "scope-user-b", "scope-b@test.local", common.RoleCommonUser, common.UserStatusEnabled, f.tenantB.Id)

	f.tokenA = &Token{UserId: f.userA.Id, TenantId: f.tenantA.Id, Key: common.GetRandomString(48), Name: "tok-a", Status: common.TokenStatusEnabled}
	f.tokenB = &Token{UserId: f.userB.Id, TenantId: f.tenantB.Id, Key: common.GetRandomString(48), Name: "tok-b", Status: common.TokenStatusEnabled}
	for _, tok := range []*Token{f.tokenA, f.tokenB} {
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed token %q: %v", tok.Name, err)
		}
	}

	now := common.GetTimestamp()
	rows := []*Log{
		// tenant A
		{UserId: f.userA.Id, TenantId: f.tenantA.Id, Username: "shared-name", TokenId: f.tokenA.Id, TokenName: "tok-a",
			Type: LogTypeConsume, ModelName: "model-a", Quota: 100, PromptTokens: 10, CompletionTokens: 20,
			Content: "sharedkw consume a", CreatedAt: now},
		{UserId: f.userA.Id, TenantId: f.tenantA.Id, Username: f.userA.Username, TokenId: f.tokenA.Id, TokenName: "tok-a",
			Type: LogTypeError, ModelName: "model-a", Content: "sharedkw error a", CreatedAt: now},
		{UserId: f.userA.Id, TenantId: f.tenantA.Id, Username: f.userA.Username,
			Type: LogTypeTopup, Content: "topup a", CreatedAt: now},
		// tenant B
		{UserId: f.userB.Id, TenantId: f.tenantB.Id, Username: "shared-name", TokenId: f.tokenB.Id, TokenName: "tok-b",
			Type: LogTypeConsume, ModelName: "model-b", Quota: 200, PromptTokens: 40, CompletionTokens: 50,
			Content: "sharedkw consume b", CreatedAt: now},
		{UserId: f.userB.Id, TenantId: f.tenantB.Id, Username: f.userB.Username, TokenId: f.tokenB.Id, TokenName: "tok-b",
			Type: LogTypeError, ModelName: "model-b", Content: "sharedkw error b", CreatedAt: now},
		{UserId: f.userB.Id, TenantId: f.tenantB.Id, Username: f.userB.Username,
			Type: LogTypeTopup, Content: "topup b", CreatedAt: now},
	}
	for i, l := range rows {
		if err := LOG_DB.Create(l).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}
	return f
}

func assertOnlyTenant(t *testing.T, fn string, logs []*Log, tenantID string) {
	t.Helper()
	for _, l := range logs {
		if l.TenantId != tenantID {
			t.Errorf("%s: cross-tenant leak — row user_id=%d tenant_id=%q, want only %q", fn, l.UserId, l.TenantId, tenantID)
		}
	}
}

func TestLogScope_GetAllLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, total, err := GetAllLogs(ForTenant(f.tenantA.Id), LogTypeUnknown, 0, 0, "", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs(ForTenant A): %v", err)
	}
	if total != 3 {
		t.Errorf("GetAllLogs(ForTenant A) total = %d, want 3", total)
	}
	assertOnlyTenant(t, "GetAllLogs", logs, f.tenantA.Id)

	// Username filter alone must not cross the tenant boundary.
	logs, total, err = GetAllLogs(ForTenant(f.tenantA.Id), LogTypeUnknown, 0, 0, "", "shared-name", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs(ForTenant A, shared username): %v", err)
	}
	if total != 1 {
		t.Errorf("GetAllLogs(ForTenant A, shared username) total = %d, want 1", total)
	}
	assertOnlyTenant(t, "GetAllLogs(username)", logs, f.tenantA.Id)

	// Platform-admin scope still spans both tenants.
	_, total, err = GetAllLogs(AllTenantsForAdmin(), LogTypeUnknown, 0, 0, "", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs(AllTenantsForAdmin): %v", err)
	}
	if total != 6 {
		t.Errorf("GetAllLogs(AllTenantsForAdmin) total = %d, want 6", total)
	}

	// Empty tenant id fails closed, never open.
	_, total, err = GetAllLogs(ForTenant(""), LogTypeUnknown, 0, 0, "", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs(ForTenant \"\"): %v", err)
	}
	if total != 0 {
		t.Errorf("GetAllLogs(ForTenant \"\") total = %d, want 0 (fail-closed)", total)
	}
}

func TestLogScope_SearchAllLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, err := SearchAllLogs(ForTenant(f.tenantA.Id), "sharedkw")
	if err != nil {
		t.Fatalf("SearchAllLogs(ForTenant A): %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("SearchAllLogs(ForTenant A) = %d rows, want 2", len(logs))
	}
	assertOnlyTenant(t, "SearchAllLogs", logs, f.tenantA.Id)

	logs, err = SearchAllLogs(AllTenantsForAdmin(), "sharedkw")
	if err != nil {
		t.Fatalf("SearchAllLogs(AllTenantsForAdmin): %v", err)
	}
	if len(logs) != 4 {
		t.Errorf("SearchAllLogs(AllTenantsForAdmin) = %d rows, want 4", len(logs))
	}

	logs, err = SearchAllLogs(ForTenant(""), "sharedkw")
	if err != nil {
		t.Fatalf("SearchAllLogs(ForTenant \"\"): %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("SearchAllLogs(ForTenant \"\") = %d rows, want 0 (fail-closed)", len(logs))
	}
}

func TestLogScope_SumUsedQuotaAndToken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	// The shared username is exactly the GetLogsSelfStat shape: same username
	// in two tenants. Only the scope keeps the aggregates apart.
	stat := SumUsedQuota(ForTenant(f.tenantA.Id), LogTypeConsume, 0, 0, "", "shared-name", "", 0, "")
	if stat.Quota != 100 {
		t.Errorf("SumUsedQuota(ForTenant A, shared-name) quota = %d, want 100 (tenant B's 200 must not bleed in)", stat.Quota)
	}
	if stat.Rpm != 1 {
		t.Errorf("SumUsedQuota(ForTenant A, shared-name) rpm = %d, want 1", stat.Rpm)
	}

	stat = SumUsedQuota(AllTenantsForAdmin(), LogTypeConsume, 0, 0, "", "shared-name", "", 0, "")
	if stat.Quota != 300 {
		t.Errorf("SumUsedQuota(AllTenantsForAdmin, shared-name) quota = %d, want 300", stat.Quota)
	}

	stat = SumUsedQuota(ForTenant(""), LogTypeConsume, 0, 0, "", "", "", 0, "")
	if stat.Quota != 0 || stat.Rpm != 0 {
		t.Errorf("SumUsedQuota(ForTenant \"\") = %+v, want zeros (fail-closed)", stat)
	}

	if got := SumUsedToken(ForTenant(f.tenantA.Id), LogTypeConsume, 0, 0, "", "shared-name", ""); got != 30 {
		t.Errorf("SumUsedToken(ForTenant A) = %d, want 30", got)
	}
	if got := SumUsedToken(AllTenantsForAdmin(), LogTypeConsume, 0, 0, "", "shared-name", ""); got != 120 {
		t.Errorf("SumUsedToken(AllTenantsForAdmin) = %d, want 120", got)
	}
}

func TestLogScope_GetUserLogsWithParams(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, total, err := GetUserLogsWithParams(ForTenant(f.tenantA.Id), &LogQueryParams{UserID: f.userA.Id, Limit: 100})
	if err != nil {
		t.Fatalf("GetUserLogsWithParams(ForTenant A, userA): %v", err)
	}
	if total != 3 {
		t.Errorf("GetUserLogsWithParams(ForTenant A, userA) total = %d, want 3", total)
	}
	assertOnlyTenant(t, "GetUserLogsWithParams", logs, f.tenantA.Id)

	// Mismatched scope/user pair yields nothing — the scope is authoritative.
	_, total, err = GetUserLogsWithParams(ForTenant(f.tenantB.Id), &LogQueryParams{UserID: f.userA.Id, Limit: 100})
	if err != nil {
		t.Fatalf("GetUserLogsWithParams(ForTenant B, userA): %v", err)
	}
	if total != 0 {
		t.Errorf("GetUserLogsWithParams(ForTenant B, userA) total = %d, want 0", total)
	}

	// The legacy params.TenantID field is ignored: setting it must not widen
	// or substitute the explicit scope (regression guard for the old
	// empty-string-means-all-tenants fail-open behavior).
	_, total, err = GetUserLogsWithParams(ForTenant(f.tenantA.Id), &LogQueryParams{UserID: f.userB.Id, TenantID: f.tenantB.Id, Limit: 100})
	if err != nil {
		t.Fatalf("GetUserLogsWithParams(ForTenant A, userB w/ params.TenantID B): %v", err)
	}
	if total != 0 {
		t.Errorf("params.TenantID must be inert; got total = %d, want 0", total)
	}
}

func TestLogScope_GetTenantLogsWithParams(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, total, err := GetTenantLogsWithParams(ForTenant(f.tenantA.Id), &LogQueryParams{Limit: 100})
	if err != nil {
		t.Fatalf("GetTenantLogsWithParams(ForTenant A): %v", err)
	}
	if total != 3 {
		t.Errorf("GetTenantLogsWithParams(ForTenant A) total = %d, want 3", total)
	}
	assertOnlyTenant(t, "GetTenantLogsWithParams", logs, f.tenantA.Id)

	// Shared username filter stays inside the tenant.
	logs, total, err = GetTenantLogsWithParams(ForTenant(f.tenantA.Id), &LogQueryParams{Username: "shared-name", Limit: 100})
	if err != nil {
		t.Fatalf("GetTenantLogsWithParams(ForTenant A, shared-name): %v", err)
	}
	if total != 1 {
		t.Errorf("GetTenantLogsWithParams(ForTenant A, shared-name) total = %d, want 1", total)
	}
	assertOnlyTenant(t, "GetTenantLogsWithParams(username)", logs, f.tenantA.Id)

	_, total, err = GetTenantLogsWithParams(ForTenant(""), &LogQueryParams{Limit: 100})
	if err != nil {
		t.Fatalf("GetTenantLogsWithParams(ForTenant \"\"): %v", err)
	}
	if total != 0 {
		t.Errorf("GetTenantLogsWithParams(ForTenant \"\") total = %d, want 0 (fail-closed)", total)
	}
}

// TestLogScope_PrincipalScopedQueries covers the exported query functions that
// isolate by principal (user_id / token_id / token key) instead of TenantScope:
// with two tenants seeded, none of them may surface tenant B rows for a
// tenant A principal.
func TestLogScope_PrincipalScopedQueries(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, total, err := GetUserLogs(f.userA.Id, LogTypeUnknown, 0, 0, "", "", 0, 100, "")
	if err != nil {
		t.Fatalf("GetUserLogs: %v", err)
	}
	if total != 3 {
		t.Errorf("GetUserLogs(userA) total = %d, want 3", total)
	}
	assertOnlyTenant(t, "GetUserLogs", logs, f.tenantA.Id)

	logs, err = SearchUserLogs(f.userA.Id, "sharedkw")
	if err != nil {
		t.Fatalf("SearchUserLogs: %v", err)
	}
	assertOnlyTenant(t, "SearchUserLogs", logs, f.tenantA.Id)

	logs, total, err = GetUserLogsInternal(f.userA.Id, 0, 100)
	if err != nil {
		t.Fatalf("GetUserLogsInternal: %v", err)
	}
	if total != 3 {
		t.Errorf("GetUserLogsInternal(userA) total = %d, want 3", total)
	}
	assertOnlyTenant(t, "GetUserLogsInternal", logs, f.tenantA.Id)

	logs, total, err = GetTokenLogsInternal(f.tokenA.Id, 0, 100)
	if err != nil {
		t.Fatalf("GetTokenLogsInternal: %v", err)
	}
	if total != 2 {
		t.Errorf("GetTokenLogsInternal(tokenA) total = %d, want 2", total)
	}
	assertOnlyTenant(t, "GetTokenLogsInternal", logs, f.tenantA.Id)

	logs, err = GetLogByKey("sk-" + f.tokenA.Key)
	if err != nil {
		t.Fatalf("GetLogByKey: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("GetLogByKey(tokenA) = %d rows, want 2", len(logs))
	}
	assertOnlyTenant(t, "GetLogByKey", logs, f.tenantA.Id)

	stats, err := GetUserLogStatByPeriod(f.userA.Id, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetUserLogStatByPeriod: %v", err)
	}
	for _, s := range stats {
		if s.Key == "model-b" {
			t.Errorf("GetUserLogStatByPeriod(userA) leaked tenant B model %q", s.Key)
		}
	}

	stats, err = GetUserLogStatInternal(f.userA.Id, "")
	if err != nil {
		t.Fatalf("GetUserLogStatInternal: %v", err)
	}
	for _, s := range stats {
		if s.Key == "model-b" {
			t.Errorf("GetUserLogStatInternal(userA) leaked tenant B model %q", s.Key)
		}
	}
}

func TestLogScope_DeleteOldLog(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	future := common.GetTimestamp() + 10_000

	// Tenant-scoped delete touches only that tenant's rows.
	deleted, err := DeleteOldLog(context.Background(), ForTenant(f.tenantA.Id), int64(future), 100)
	if err != nil {
		t.Fatalf("DeleteOldLog(ForTenant A): %v", err)
	}
	if deleted != 3 {
		t.Errorf("DeleteOldLog(ForTenant A) deleted = %d, want 3", deleted)
	}
	var remaining int64
	LOG_DB.Model(&Log{}).Where("tenant_id = ?", f.tenantB.Id).Count(&remaining)
	if remaining != 3 {
		t.Errorf("tenant B rows after ForTenant(A) delete = %d, want 3 (untouched)", remaining)
	}

	// Platform retention (admin scope) removes the rest.
	deleted, err = DeleteOldLog(context.Background(), AllTenantsForAdmin(), int64(future), 100)
	if err != nil {
		t.Fatalf("DeleteOldLog(AllTenantsForAdmin): %v", err)
	}
	if deleted != 3 {
		t.Errorf("DeleteOldLog(AllTenantsForAdmin) deleted = %d, want 3", deleted)
	}
}

// newTenantScopeGinCtx builds a bare gin context; tenantID == "" simulates the
// plain /v1 relay path where TokenAuth injects no tenant context.
func newTenantScopeGinCtx(username, tenantID string) *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	if username != "" {
		c.Set("username", username)
	}
	if tenantID != "" {
		c.Set("tenant_id", tenantID)
	}
	return c
}

// TestLogWrite_StampsOwnerTenant guards resolveLogTenantID: rows written
// without a gin tenant context must be stamped with the owning user's tenant,
// not silently attributed to 'default' (which both polluted the default
// tenant's log views and hid the rows from the owning tenant's).
func TestLogWrite_StampsOwnerTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	t.Run("RecordLog uses owning user's tenant", func(t *testing.T) {
		RecordLog(f.userB.Id, LogTypeManage, "manage op b")
		var lg Log
		if err := LOG_DB.Where("user_id = ? AND type = ?", f.userB.Id, LogTypeManage).First(&lg).Error; err != nil {
			t.Fatalf("manage log not written: %v", err)
		}
		if lg.TenantId != f.tenantB.Id {
			t.Errorf("RecordLog tenant_id = %q, want %q", lg.TenantId, f.tenantB.Id)
		}
	})

	t.Run("RecordLog system row (userId 0) stays default", func(t *testing.T) {
		RecordLog(0, LogTypeSystem, "system op")
		var lg Log
		if err := LOG_DB.Where("user_id = 0 AND type = ?", LogTypeSystem).First(&lg).Error; err != nil {
			t.Fatalf("system log not written: %v", err)
		}
		if lg.TenantId != "default" {
			t.Errorf("system RecordLog tenant_id = %q, want default", lg.TenantId)
		}
	})

	t.Run("RecordErrorLog without gin tenant falls back to user tenant", func(t *testing.T) {
		c := newTenantScopeGinCtx(f.userB.Username, "")
		RecordErrorLog(c, f.userB.Id, 7, "model-b", "tok-b", "upstream 500", f.tokenB.Id, 1, false, "default", nil)
		var lg Log
		if err := LOG_DB.Where("user_id = ? AND type = ?", f.userB.Id, LogTypeError).Order("id desc").First(&lg).Error; err != nil {
			t.Fatalf("error log not written: %v", err)
		}
		if lg.TenantId != f.tenantB.Id {
			t.Errorf("RecordErrorLog tenant_id = %q, want %q", lg.TenantId, f.tenantB.Id)
		}
	})

	t.Run("RecordConsumeLog: gin tenant wins, else user tenant", func(t *testing.T) {
		prevConsume := common.LogConsumeEnabled
		prevExport := common.DataExportEnabled
		common.LogConsumeEnabled = true
		common.DataExportEnabled = false
		defer func() {
			common.LogConsumeEnabled = prevConsume
			common.DataExportEnabled = prevExport
		}()

		params := RecordConsumeLogParams{
			ChannelId: 1, PromptTokens: 1, CompletionTokens: 1, ModelName: "model-b",
			TokenName: "tok-b", TokenId: f.tokenB.Id, Quota: 5, ChannelType: 1, Content: "consume",
		}

		// No gin tenant (plain /v1 relay shape) → owning user's tenant.
		c := newTenantScopeGinCtx(f.userB.Username, "")
		RecordConsumeLog(c, f.userB.Id, params)
		var lg Log
		if err := LOG_DB.Where("user_id = ? AND type = ? AND content = ?", f.userB.Id, LogTypeConsume, "consume").
			Order("id desc").First(&lg).Error; err != nil {
			t.Fatalf("consume log not written: %v", err)
		}
		if lg.TenantId != f.tenantB.Id {
			t.Errorf("RecordConsumeLog (no gin tenant) tenant_id = %q, want %q", lg.TenantId, f.tenantB.Id)
		}

		// Explicit gin tenant stays authoritative. Fresh dest var: First on a
		// struct with a non-zero primary key adds `id = ?` to the query.
		c = newTenantScopeGinCtx(f.userB.Username, "explicit-tenant")
		p := params
		p.Content = "consume explicit"
		RecordConsumeLog(c, f.userB.Id, p)
		var lg2 Log
		if err := LOG_DB.Where("user_id = ? AND type = ? AND content = ?", f.userB.Id, LogTypeConsume, "consume explicit").
			Order("id desc").First(&lg2).Error; err != nil {
			t.Fatalf("consume log (explicit tenant) not written: %v", err)
		}
		if lg2.TenantId != "explicit-tenant" {
			t.Errorf("RecordConsumeLog (gin tenant) tenant_id = %q, want explicit-tenant", lg2.TenantId)
		}
	})
}
