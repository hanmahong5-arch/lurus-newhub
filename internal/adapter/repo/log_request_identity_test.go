package repo

// log_request_identity_test.go — L2-REQUEST-IDENTITY read side: request_id/
// session_id filters on the v2 log query functions, and the single-row
// GET /v1/generation lookup (GetLogByRequestID) with ownership stricter than
// GetLogByKey (log_by_key_ownership_test.go): there is no caller-supplied
// key to resolve first, so the query filters on the bearer's own
// token/tenant/user directly.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestGetUserLogsWithParams_SessionIdFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "session-filter-user", "sessionfilter@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-sessionfilter")
	now := common.GetTimestamp()
	rows := []*Log{
		{UserId: u.Id, TenantId: u.TenantId, Type: LogTypeConsume, Quota: 10, CreatedAt: now, Other: `{"session_id":"conv-1"}`},
		{UserId: u.Id, TenantId: u.TenantId, Type: LogTypeConsume, Quota: 20, CreatedAt: now, Other: `{"session_id":"conv-2"}`},
	}
	for i, r := range rows {
		if err := LOG_DB.Create(r).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	filtered, total, err := GetUserLogsWithParams(ForTenant(u.TenantId), &LogQueryParams{
		UserID: u.Id, SessionID: "conv-1", Offset: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetUserLogsWithParams(session_id=conv-1): %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want exactly one of two rows for session_id=conv-1", total)
	}
	if len(filtered) != 1 || filtered[0].Quota != 10 {
		t.Errorf("filtered rows = %+v, want the single conv-1 row (quota=10)", filtered)
	}
}

func TestGetTenantLogsWithParams_RequestIdFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tenant := seedTenant(t, "tenant-reqid-admin", "tenant-reqid-admin", "ReqId Admin Tenant")
	u := seedUser(t, "reqid-admin-user", "reqidadmin@test.local", common.RoleCommonUser, common.UserStatusEnabled, tenant.Id)
	now := common.GetTimestamp()
	rows := []*Log{
		{UserId: u.Id, TenantId: tenant.Id, Type: LogTypeConsume, Quota: 5, CreatedAt: now, Other: `{"request_id":"req-aaa"}`},
		{UserId: u.Id, TenantId: tenant.Id, Type: LogTypeConsume, Quota: 7, CreatedAt: now, Other: `{"request_id":"req-bbb"}`},
	}
	for i, r := range rows {
		if err := LOG_DB.Create(r).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	logs, total, err := GetTenantLogsWithParams(ForTenant(tenant.Id), &LogQueryParams{
		RequestID: "req-aaa", Offset: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetTenantLogsWithParams(request_id=req-aaa): %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].Quota != 5 {
		t.Errorf("logs = %+v total=%d, want exactly the quota=5 req-aaa row", logs, total)
	}
}

func TestGetLogByRequestID_ReturnsOwnRow(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "genlookup-user", "genlookup@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	now := common.GetTimestamp()
	row := &Log{
		UserId: u.Id, TenantId: "default", TokenId: tok.Id, Type: LogTypeConsume,
		ModelName: "gpt-4o", Quota: 42, CreatedAt: now, Other: `{"request_id":"uat-abc12345","session_id":"conv-42"}`,
	}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	got, err := GetLogByRequestID("uat-abc12345", u.Id, "default", tok.Id)
	if err != nil {
		t.Fatalf("GetLogByRequestID: %v", err)
	}
	if got.Quota != 42 || got.Id != row.Id {
		t.Errorf("got = %+v, want the seeded row (quota=42, id=%d)", got, row.Id)
	}
}

func TestGetLogByRequestID_ForeignTokenDenied(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	owner := seedUser(t, "genlookup-owner", "genlookup-owner@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	ownerTok := seedToken(t, owner.Id, common.TokenStatusEnabled, true, 0, -1)
	intruder := seedUser(t, "genlookup-intruder", "genlookup-intruder@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	intruderTok := seedToken(t, intruder.Id, common.TokenStatusEnabled, true, 0, -1)

	row := &Log{
		UserId: owner.Id, TenantId: "default", TokenId: ownerTok.Id, Type: LogTypeConsume,
		ModelName: "gpt-4o", Quota: 42, CreatedAt: common.GetTimestamp(), Other: `{"request_id":"uat-owned-only"}`,
	}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	// Right request id, but presented with the INTRUDER's own token/user —
	// must deny exactly like "never happened", not leak that the row exists.
	_, err := GetLogByRequestID("uat-owned-only", intruder.Id, "default", intruderTok.Id)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("err = %v, want gorm.ErrRecordNotFound for a foreign token", err)
	}
}

func TestGetLogByRequestID_UnknownIdReturnsNotFound(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "genlookup-empty-user", "genlookup-empty@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	_, err := GetLogByRequestID("no-such-request-id", u.Id, "default", tok.Id)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("err = %v, want gorm.ErrRecordNotFound", err)
	}
}

// TestGetLogByRequestID_SameUserOtherTokenDenied pins the "stricter than
// GetLogByKey" ownership rule the spec calls for: GetLogByKey scopes by
// (user, tenant) because it starts from a caller-supplied key already proven
// to belong to them; GetLogByRequestID has no such external key, only the
// bearer token of THIS call, so it must also match token_id — a compromised
// token must not read a sibling token's history just because they share an
// owning user.
func TestGetLogByRequestID_SameUserOtherTokenDenied(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "genlookup-twotoken-user", "genlookup-twotoken@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tokenA := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	tokenB := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	row := &Log{
		UserId: u.Id, TenantId: "default", TokenId: tokenA.Id, Type: LogTypeConsume,
		ModelName: "gpt-4o", Quota: 42, CreatedAt: common.GetTimestamp(), Other: `{"request_id":"uat-tokenA-only"}`,
	}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	// Same user, but presented via tokenB (a different, legitimately-owned
	// token) — must still deny.
	_, err := GetLogByRequestID("uat-tokenA-only", u.Id, "default", tokenB.Id)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("err = %v, want gorm.ErrRecordNotFound when the bearer token differs, even for the same owning user", err)
	}

	// Sanity: tokenA itself still resolves it.
	got, err := GetLogByRequestID("uat-tokenA-only", u.Id, "default", tokenA.Id)
	if err != nil {
		t.Fatalf("GetLogByRequestID via the correct token: %v", err)
	}
	if got.Quota != 42 {
		t.Errorf("got.Quota = %d, want 42", got.Quota)
	}
}

// TestRecordErrorLog_StampsRequestIdFromContext locks findings item
// L2-REQUEST-IDENTITY#4: RecordErrorLog is one of the two writers
// setRequestIdIfAbsent funnels through (the other is RecordConsumeLog,
// covered by relay_attribution_test.go's success path); this pins the
// pre-channel error-row side directly at the repo layer — an authenticated
// caller whose request never reaches a channel (e.g. a middleware 401/403)
// still gets a row carrying other.request_id, so /v1/generation-style
// reconciliation works for rejections too.
func TestRecordErrorLog_StampsRequestIdFromContext(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "errlog-reqid-user", "errlog-reqid@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "uat-errlog-reqid")

	RecordErrorLog(c, u.Id, 0, "gpt-4o", "prod-key", "upstream 500", tok.Id, 1, false, "default", nil)

	var lg Log
	if err := LOG_DB.Where("user_id = ? AND type = ?", u.Id, LogTypeError).Order("id desc").First(&lg).Error; err != nil {
		t.Fatalf("error log not written: %v", err)
	}
	if !strings.Contains(lg.Other, `"request_id":"uat-errlog-reqid"`) {
		t.Errorf("error row Other = %q, want other.request_id = uat-errlog-reqid", lg.Other)
	}
}
