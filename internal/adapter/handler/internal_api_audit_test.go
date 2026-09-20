package handler

// internal_api_audit_test.go — cycle 13 L4 (V1DOORS/SECURITY-14) oracle: the
// four v1 internal-API-key CRUD handlers (AdminCreateApiKey / Update / Delete
// / Toggle, internal_api.go) and DeleteHistoryLogs (log.go) previously wrote
// zero audit trail. Each test below drives the real handler function against
// a hermetic sqlite DB with a real governance.AuditWriter bound to it
// (mirrors setupPricingWriteRouter's pinnedAuditWriter wiring in
// v2_pricing_write_test.go) and asserts the resulting audit_events row.
//
// DeleteHistoryLogs has no owned/created test file of its own in the L4 plan
// section (log.go's only listed test companion is internal_api_test.go,
// which is about a different handler family) — its oracle lives here because
// this file already wires the hermetic DB + pinned audit writer both
// handlers need, and because handler.log_test.go does not exist and is not
// in this lane's Created list.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// internalKeyAuditTestDBCounter names this file's :memory: sqlite databases.
// Scoped to this file — the package's other fixtures (testDBCounter,
// v2TestDBCounter, pricingWriteTestDBCounter, ikadmin's local counter in
// internal_api_key_admin_v2_test.go) each keep their own counter, so a
// shared one is not needed and would only add a cross-file coupling.
var internalKeyAuditTestDBCounter atomic.Int64

type internalKeyAuditFixture struct {
	DB      *gorm.DB
	Cleanup func()
}

// setupInternalKeyAuditFixture wires a hermetic sqlite DB carrying
// InternalApiKey, Log, AuditEvent and AuditChainHead, points the repo
// package globals at it, and installs a real governance.AuditWriter bound to
// the same handle via pinAuditWriter (audit_writer_pin_test.go) so a write
// this fixture's cleanup is about to invalidate never outlives its database.
func setupInternalKeyAuditFixture(t *testing.T) *internalKeyAuditFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:ikaudit%d?mode=memory&cache=shared", internalKeyAuditTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.InternalApiKey{}, &repo.Log{}, &entity.AuditEvent{}, &entity.AuditChainHead{},
	} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	pinAuditWriter(t, db)

	cleanup := func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		if sqlDB, errDB := db.DB(); errDB == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(cleanup)
	return &internalKeyAuditFixture{DB: db, Cleanup: cleanup}
}

// seedAuditTestInternalApiKey persists an InternalApiKey row directly (not
// through AdminCreateApiKey) so the Update/Delete/Toggle tests exercise only
// the handler under test, not a chain through Create first.
func seedAuditTestInternalApiKey(t *testing.T, db *gorm.DB, name string) *repo.InternalApiKey {
	t.Helper()
	scopesJSON, _ := json.Marshal([]string{repo.ScopeProvisioning})
	key := &repo.InternalApiKey{
		Name:      name,
		KeyHash:   hashTestKey("lurus_ik_seed_" + name),
		KeyPrefix: "lurus_ik_seed",
		Scopes:    string(scopesJSON),
		Enabled:   true,
	}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed internal api key: %v", err)
	}
	return key
}

// ─── AdminCreateApiKey ──────────────────────────────────────────────────────

// TestAdminCreateApiKey_RecordsInternalKeyCreatedAudit_WithoutRawKey is the L4
// plan's named oracle: root POST /api/api-keys/ produces exactly one
// internal_key.created row, and the raw key the response hands back never
// appears in that row's Details.
func TestAdminCreateApiKey_RecordsInternalKeyCreatedAudit_WithoutRawKey(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)

	c, w := v1Ctx(http.MethodPost, "/api/api-keys/", map[string]interface{}{
		"name":   "ci-provisioner",
		"scopes": []string{repo.ScopeProvisioning},
	}, common.RoleRootUser, "default", 1)
	AdminCreateApiKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body=%s", w.Body.String())
	}
	rawKey, _ := data["key"].(string)
	if rawKey == "" || !strings.HasPrefix(rawKey, "lurus_ik_") {
		t.Fatalf("expected a raw lurus_ik_ key in the response, got %v", data["key"])
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionInternalKeyCreated, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 internal_key.created row, got %d", len(events))
	}
	ev := events[0]
	if ev.Resource != governance.ResourceInternalKey {
		t.Errorf("Resource = %q, want %q", ev.Resource, governance.ResourceInternalKey)
	}
	if ev.ActorID != 1 {
		t.Errorf("ActorID = %d, want 1", ev.ActorID)
	}
	if strings.Contains(ev.Details, rawKey) {
		t.Fatalf("audit Details leaked the raw key: %s", ev.Details)
	}
	if !strings.Contains(ev.Details, "ci-provisioner") {
		t.Errorf("Details missing key name: %s", ev.Details)
	}
	_ = f // fixture installs globals as a side effect; no direct field use here.
}

// ─── AdminUpdateApiKey ──────────────────────────────────────────────────────

func TestAdminUpdateApiKey_RecordsInternalKeyUpdatedAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key := seedAuditTestInternalApiKey(t, f.DB, "update-target")

	c, w := v1Ctx(http.MethodPut, fmt.Sprintf("/api/api-keys/%d", key.Id), map[string]interface{}{
		"name":   "renamed-key",
		"scopes": []string{repo.ScopeUserRead},
	}, common.RoleRootUser, "default", 1)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(key.Id)}}
	AdminUpdateApiKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionInternalKeyUpdated, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 internal_key.updated row, got %d", len(events))
	}
	ev := events[0]
	if ev.ResourceID != key.Id {
		t.Errorf("ResourceID = %d, want %d", ev.ResourceID, key.Id)
	}
	if !strings.Contains(ev.Details, "renamed-key") {
		t.Errorf("Details missing new name: %s", ev.Details)
	}
}

// ─── AdminDeleteApiKey ──────────────────────────────────────────────────────

func TestAdminDeleteApiKey_RecordsInternalKeyDeletedAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key := seedAuditTestInternalApiKey(t, f.DB, "delete-target")

	c, w := v1Ctx(http.MethodDelete, fmt.Sprintf("/api/api-keys/%d", key.Id), nil,
		common.RoleRootUser, "default", 1)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(key.Id)}}
	AdminDeleteApiKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionInternalKeyDeleted, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 internal_key.deleted row, got %d", len(events))
	}
	if events[0].ResourceID != key.Id {
		t.Errorf("ResourceID = %d, want %d", events[0].ResourceID, key.Id)
	}
}

// ─── AdminToggleApiKey ──────────────────────────────────────────────────────

func TestAdminToggleApiKey_RecordsInternalKeyToggledAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key := seedAuditTestInternalApiKey(t, f.DB, "toggle-target")

	c, w := v1Ctx(http.MethodPut, fmt.Sprintf("/api/api-keys/%d/toggle", key.Id), nil,
		common.RoleRootUser, "default", 1)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(key.Id)}}
	AdminToggleApiKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionInternalKeyToggled, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 internal_key.toggled row, got %d", len(events))
	}
	if events[0].ResourceID != key.Id {
		t.Errorf("ResourceID = %d, want %d", events[0].ResourceID, key.Id)
	}
}

// ─── DeleteHistoryLogs ──────────────────────────────────────────────────────

// TestDeleteHistoryLogs_RecordsLogsPurgedAudit_RowSurvivesPurge is the L4
// plan's named oracle for logs.purged: a role-10 (tenant admin, not root)
// DELETE /api/log/ produces one logs.purged row carrying the deleted count,
// and a logs.purged row seeded BEFORE the purge (standing in for a prior
// purge's history) is still present afterward — proving DeleteOldLog's
// delete only ever touches the logs table, never audit_events.
func TestDeleteHistoryLogs_RecordsLogsPurgedAudit_RowSurvivesPurge(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	const tenantID = "tenant-purge-test"

	now := common.GetTimestamp()
	cutoff := now - 500
	for i := 0; i < 3; i++ {
		if err := f.DB.Create(&repo.Log{
			UserId: 7, TenantId: tenantID, CreatedAt: cutoff - 10 - int64(i), Type: repo.LogTypeConsume,
		}).Error; err != nil {
			t.Fatalf("seed old log %d: %v", i, err)
		}
	}
	// Outside the cutoff — must survive.
	if err := f.DB.Create(&repo.Log{
		UserId: 7, TenantId: tenantID, CreatedAt: now, Type: repo.LogTypeConsume,
	}).Error; err != nil {
		t.Fatalf("seed recent log: %v", err)
	}
	// A different tenant's old row — must survive a tenant-scoped purge.
	if err := f.DB.Create(&repo.Log{
		UserId: 8, TenantId: "other-tenant", CreatedAt: cutoff - 20, Type: repo.LogTypeConsume,
	}).Error; err != nil {
		t.Fatalf("seed other-tenant log: %v", err)
	}

	// Stand-in for a prior purge's audit history, written through the real
	// RecordAuditEvent path (NewDetachedAuditEvent — no gin.Context needed).
	governance.RecordAuditEvent(governance.NewDetachedAuditEvent(tenantID, governance.ActorAdmin, 1,
		governance.ActionLogsPurged, governance.ResourceLog, 0, `{"cutoff":1,"scope":"prior","deleted":9}`))
	preEvents, _, err := repo.GetAuditEvents("", governance.ActionLogsPurged, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents (pre-seed check): %v", err)
	}
	if len(preEvents) != 1 {
		t.Fatalf("expected 1 pre-seeded logs.purged row, got %d", len(preEvents))
	}

	c, w := v1Ctx(http.MethodDelete, fmt.Sprintf("/api/log/?target_timestamp=%d", cutoff), nil,
		common.RoleAdminUser, tenantID, 42)
	DeleteHistoryLogs(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if success, _ := body["success"].(bool); !success {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	deleted, _ := body["data"].(float64)
	if int(deleted) != 3 {
		t.Fatalf("data (deleted count) = %v, want 3", body["data"])
	}

	var remaining int64
	f.DB.Model(&repo.Log{}).Count(&remaining)
	if remaining != 2 {
		t.Fatalf("expected 2 surviving log rows (1 recent same-tenant + 1 other-tenant), got %d", remaining)
	}

	afterEvents, _, err := repo.GetAuditEvents("", governance.ActionLogsPurged, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents (post-purge check): %v", err)
	}
	if len(afterEvents) != 2 {
		t.Fatalf("expected 2 logs.purged rows after purge (1 pre-seeded + 1 new), got %d", len(afterEvents))
	}
	foundPrior, foundNew := false, false
	for _, ev := range afterEvents {
		if strings.Contains(ev.Details, `"deleted":9`) {
			foundPrior = true
		}
		if strings.Contains(ev.Details, `"deleted":3`) {
			foundNew = true
			if !strings.Contains(ev.Details, tenantID) {
				t.Errorf("new row's Details missing tenant scope: %s", ev.Details)
			}
			if ev.ActorID != 42 {
				t.Errorf("new row ActorID = %d, want 42", ev.ActorID)
			}
		}
	}
	if !foundPrior {
		t.Fatalf("pre-seeded logs.purged row vanished after the purge — DeleteOldLog must have touched audit_events")
	}
	if !foundNew {
		t.Fatalf("no new logs.purged row with deleted:3 found among: %+v", afterEvents)
	}
}
