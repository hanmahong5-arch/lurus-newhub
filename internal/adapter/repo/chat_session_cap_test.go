package repo

// chat_session_cap_test.go — repo-level tests for CreateChatSession's
// MaxChatSessionsPerUser cap (cycle-11 L5). Self-contained in-memory SQLite
// setup (same pattern as chat_session.go's own handler-package twin,
// v2_chat_session_test.go) rather than sqlite_testutil_test.go's shared
// helper, because that helper's table list does not include ChatSession/
// ChatMessage and is owned by another lane this cycle.

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var chatSessionCapTestDBCounter atomic.Int64

func setupChatSessionCapDB(t *testing.T) (tenantID string, userA, userB int) {
	t.Helper()

	n := chatSessionCapTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:chatsessioncap%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&User{}, &entity.Tenant{}, &entity.ChatSession{}, &entity.ChatMessage{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("automigrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB := DB, LOG_DB
	DB = db
	LOG_DB = db
	InitCol()

	tid := fmt.Sprintf("cap-tenant-%d", n)
	tenant := &entity.Tenant{
		Id: tid, IDPOrgID: "cap-org-" + tid, Slug: fmt.Sprintf("cap-slug-%d", n),
		Name: "Cap Tenant", Status: entity.TenantStatusEnabled,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	a := &User{
		Username: fmt.Sprintf("cap-user-a-%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("cap-a-%d@test.local", n), TenantId: tid,
	}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed user a: %v", err)
	}
	b := &User{
		Username: fmt.Sprintf("cap-user-b-%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("cap-b-%d@test.local", n), TenantId: tid,
	}
	if err := db.Create(b).Error; err != nil {
		t.Fatalf("seed user b: %v", err)
	}

	t.Cleanup(func() {
		DB, LOG_DB = prevDB, prevLogDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return tid, a.Id, b.Id
}

// TestCreateChatSession_RejectsWhenSessionCapReached seeds a caller up to
// MaxChatSessionsPerUser sessions directly (bypassing the transaction, for
// test speed) and drives the real CreateChatSession for the 201st: it must
// fail with ErrChatSessionLimitReached, and a DIFFERENT user in the same
// tenant must still succeed — the cap is per (tenant_id, user_id), not
// per tenant.
func TestCreateChatSession_RejectsWhenSessionCapReached(t *testing.T) {
	tenantID, userA, userB := setupChatSessionCapDB(t)

	rows := make([]entity.ChatSession, MaxChatSessionsPerUser)
	for i := range rows {
		rows[i] = entity.ChatSession{TenantId: tenantID, UserId: userA, Title: fmt.Sprintf("seed-%d", i), Model: "rt-alpha"}
	}
	if err := DB.Create(&rows).Error; err != nil {
		t.Fatalf("seed %d sessions: %v", MaxChatSessionsPerUser, err)
	}

	if _, err := CreateChatSession(tenantID, userA, "one too many", "rt-alpha", nil); !errors.Is(err, ErrChatSessionLimitReached) {
		t.Fatalf("create at cap: err = %v, want ErrChatSessionLimitReached", err)
	}

	if _, err := CreateChatSession(tenantID, userB, "different user", "rt-alpha", nil); err != nil {
		t.Fatalf("create for a different user in the same tenant: err = %v, want nil", err)
	}
}

// TestCreateChatSession_CapCountsOnlyCallerRows proves the COUNT the cap
// runs is scoped by user_id, not just tenant_id: userB, who owns zero
// sessions, can still create one even though userA (same tenant) is already
// at the cap.
func TestCreateChatSession_CapCountsOnlyCallerRows(t *testing.T) {
	tenantID, userA, userB := setupChatSessionCapDB(t)

	rows := make([]entity.ChatSession, MaxChatSessionsPerUser)
	for i := range rows {
		rows[i] = entity.ChatSession{TenantId: tenantID, UserId: userA, Title: fmt.Sprintf("seed-%d", i), Model: "rt-alpha"}
	}
	if err := DB.Create(&rows).Error; err != nil {
		t.Fatalf("seed %d sessions for userA: %v", MaxChatSessionsPerUser, err)
	}

	session, err := CreateChatSession(tenantID, userB, "userB's first", "rt-alpha", nil)
	if err != nil {
		t.Fatalf("create for userB: err = %v, want nil", err)
	}
	if session.UserId != userB {
		t.Fatalf("created session UserId = %d, want %d", session.UserId, userB)
	}
}

// TestMaxChatSessionsPerUserIsThe200TheCommentAdvertises pins the actual
// enforced value: the two tests above derive their seed count from the
// constant itself, so silently lowering it (e.g. to 5) would keep both
// green while the cycle-11 plan's advertised cap of 200 quietly stopped
// being true. This is the one place that fails if that happens.
func TestMaxChatSessionsPerUserIsThe200TheCommentAdvertises(t *testing.T) {
	if MaxChatSessionsPerUser != 200 {
		t.Fatalf("MaxChatSessionsPerUser = %d, want 200", MaxChatSessionsPerUser)
	}
}
