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
//
// The plan named a real-chain oracle (through SetApiRouter). That is not
// reachable from package handler: internal/adapter/handler/router imports
// handler, so a handler-package test importing router would be an import
// cycle, and router/*_test.go belongs to the serial wiring lane W. The
// handlers are therefore driven directly through v1Ctx
// (v1_cross_tenant_idor_test.go), which sets exactly the keys the auth
// middleware sets (role / tenant_id / id). The real-chain half — that these
// routes are registered and that the AST walk finds their
// governance.RecordAuditEvent calls — is
// router/audit_coverage_test.go's TestAdminWriteRoutesAreAudited, together
// with the six entries this lane added to audit_coverage_gen.go.

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
// the handler under test, not a chain through Create first. It returns the
// row and the raw key string behind its hash, which the caller feeds to
// assertAuditRowsCarryNoKeyMaterial.
func seedAuditTestInternalApiKey(t *testing.T, db *gorm.DB, name string) (*repo.InternalApiKey, string) {
	t.Helper()
	scopesJSON, _ := json.Marshal([]string{repo.ScopeProvisioning})
	rawKey := "lurus_ik_seed_" + name
	key := &repo.InternalApiKey{
		Name:      name,
		KeyHash:   hashTestKey(rawKey),
		KeyPrefix: "lurus_ik_seed",
		Scopes:    string(scopesJSON),
		Enabled:   true,
	}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed internal api key: %v", err)
	}
	return key, rawKey
}

// assertAuditRowsCarryNoKeyMaterial scans EVERY audit_events row the test
// produced — not only the row the caller asserted on — for the key material
// named in secrets, and fails on the first hit. It is what makes the
// taxonomy comment's "Details never carry the raw key" (audit_action.go,
// internal-key block) an enforced claim for all four internal-key handlers
// rather than an assertion in one of them.
//
// What is deliberately NOT a secret here: InternalApiKey.KeyPrefix, the
// 16-char display prefix (`lurus_ik_` + the first 7 characters of the random
// tail). AdminListApiKeys already returns it to the same caller, and
// AdminCreateApiKey puts it in Details on purpose so an operator can match a
// trail row to a listed key. Callers pass the remainder — the part of the
// raw key the prefix does not reveal — so a Details string that carried the
// whole key would still be caught.
//
// Fail-fast: zero rows means the handler under test wrote nothing and the
// scan proved nothing, so that is a failure, not a pass.
func assertAuditRowsCarryNoKeyMaterial(t *testing.T, db *gorm.DB, secrets ...string) {
	t.Helper()
	if len(secrets) == 0 {
		t.Fatalf("assertAuditRowsCarryNoKeyMaterial called with no secrets to look for")
	}
	var rows []entity.AuditEvent
	if err := db.Model(&entity.AuditEvent{}).Find(&rows).Error; err != nil {
		t.Fatalf("read audit_events: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no audit_events rows to scan — the handler under test wrote none")
	}
	for _, secret := range secrets {
		if secret == "" {
			t.Fatalf("empty secret passed in: the fixture did not produce the material this scan is meant to pin")
		}
		for _, ev := range rows {
			if strings.Contains(ev.Details, secret) {
				t.Fatalf("audit row %s (id=%d) leaked key material %q: %s", ev.Action, ev.ID, secret, ev.Details)
			}
		}
	}
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
	// Every row, not just this one: the raw key, the part of it the stored
	// 16-char display prefix does not reveal, and the stored hash.
	var stored repo.InternalApiKey
	if err := f.DB.Where("name = ?", "ci-provisioner").First(&stored).Error; err != nil {
		t.Fatalf("read back the created key row: %v", err)
	}
	if len(stored.KeyPrefix) >= len(rawKey) {
		t.Fatalf("KeyPrefix %q is not shorter than the raw key — the remainder below would be empty", stored.KeyPrefix)
	}
	assertAuditRowsCarryNoKeyMaterial(t, f.DB, rawKey, rawKey[len(stored.KeyPrefix):], stored.KeyHash)
}

// TestAdminCreateApiKey_WildcardRefusedForNonRoot_RecordsScopeRejectedAudit
// pins the refusal half (cycle 13 L4, D-L4-4): a role-10 admin asking for the
// wildcard scope is refused 403 by the guard in AdminCreateApiKey, creates no
// key, and leaves exactly one auth.scope_rejected row on resource
// internal_key. Without it the grant is audited while the attempt is not.
func TestAdminCreateApiKey_WildcardRefusedForNonRoot_RecordsScopeRejectedAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)

	c, w := v1Ctx(http.MethodPost, "/api/api-keys/", map[string]interface{}{
		"name":   "sneaky-wildcard",
		"scopes": []string{repo.ScopeUserRead, repo.ScopeAll},
	}, common.RoleAdminUser, "tenant-a", 77)
	AdminCreateApiKey(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("expected success=false on the refusal, body=%s", w.Body.String())
	}

	var keyCount int64
	f.DB.Model(&repo.InternalApiKey{}).Count(&keyCount)
	if keyCount != 0 {
		t.Fatalf("refused create still persisted %d internal key row(s)", keyCount)
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionAuthScopeRejected, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 auth.scope_rejected row, got %d", len(events))
	}
	ev := events[0]
	if ev.Resource != governance.ResourceInternalKey {
		t.Errorf("Resource = %q, want %q", ev.Resource, governance.ResourceInternalKey)
	}
	if ev.ActorID != 77 {
		t.Errorf("ActorID = %d, want 77", ev.ActorID)
	}
	if ev.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q, want tenant-a", ev.TenantID)
	}
	for _, want := range []string{`"op":"create"`, "sneaky-wildcard", `"requested_scope":"*"`} {
		if !strings.Contains(ev.Details, want) {
			t.Errorf("Details missing %s: %s", want, ev.Details)
		}
	}

	granted, _, err := repo.GetAuditEvents("", governance.ActionInternalKeyCreated, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents (created): %v", err)
	}
	if len(granted) != 0 {
		t.Fatalf("a refused create still produced %d internal_key.created row(s)", len(granted))
	}
}

// ─── AdminUpdateApiKey ──────────────────────────────────────────────────────

func TestAdminUpdateApiKey_RecordsInternalKeyUpdatedAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key, seededRawKey := seedAuditTestInternalApiKey(t, f.DB, "update-target")

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
	assertAuditRowsCarryNoKeyMaterial(t, f.DB, seededRawKey, key.KeyHash)
}

// TestAdminUpdateApiKey_WildcardRefusedForNonRoot_RecordsScopeRejectedAudit is
// the same refusal oracle for the update path, where the key already exists:
// the row carries its id, the stored name is unchanged, and no
// internal_key.updated row appears.
func TestAdminUpdateApiKey_WildcardRefusedForNonRoot_RecordsScopeRejectedAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key, seededRawKey := seedAuditTestInternalApiKey(t, f.DB, "escalation-target")

	c, w := v1Ctx(http.MethodPut, fmt.Sprintf("/api/api-keys/%d", key.Id), map[string]interface{}{
		"name":   "escalated",
		"scopes": []string{repo.ScopeAll},
	}, common.RoleAdminUser, "tenant-a", 77)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(key.Id)}}
	AdminUpdateApiKey(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("expected success=false on the refusal, body=%s", w.Body.String())
	}

	var after repo.InternalApiKey
	if err := f.DB.First(&after, key.Id).Error; err != nil {
		t.Fatalf("read back the key row: %v", err)
	}
	if after.Name != "escalation-target" || strings.Contains(after.Scopes, repo.ScopeAll) {
		t.Fatalf("refused update still mutated the row: name=%q scopes=%q", after.Name, after.Scopes)
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionAuthScopeRejected, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 auth.scope_rejected row, got %d", len(events))
	}
	ev := events[0]
	if ev.Resource != governance.ResourceInternalKey {
		t.Errorf("Resource = %q, want %q", ev.Resource, governance.ResourceInternalKey)
	}
	if ev.ResourceID != key.Id {
		t.Errorf("ResourceID = %d, want %d", ev.ResourceID, key.Id)
	}
	for _, want := range []string{`"op":"update"`, "escalated", `"requested_scope":"*"`} {
		if !strings.Contains(ev.Details, want) {
			t.Errorf("Details missing %s: %s", want, ev.Details)
		}
	}

	updated, _, err := repo.GetAuditEvents("", governance.ActionInternalKeyUpdated, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents (updated): %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("a refused update still produced %d internal_key.updated row(s)", len(updated))
	}
	assertAuditRowsCarryNoKeyMaterial(t, f.DB, seededRawKey, key.KeyHash)
}

// ─── AdminDeleteApiKey ──────────────────────────────────────────────────────

func TestAdminDeleteApiKey_RecordsInternalKeyDeletedAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key, seededRawKey := seedAuditTestInternalApiKey(t, f.DB, "delete-target")

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
	// A hard delete leaves no row to resolve the id against, so the audit
	// row must carry the snapshot itself (cycle-13 hand-finish after L4).
	scopesJSON, _ := json.Marshal([]string{repo.ScopeProvisioning})
	for _, want := range []string{`"found":true`, `"name":"delete-target"`, `"key_prefix":"lurus_ik_seed"`, `"scopes":` + string(scopesJSON), `"enabled":true`} {
		if !strings.Contains(events[0].Details, want) {
			t.Errorf("internal_key.deleted Details = %s, want it to carry %s", events[0].Details, want)
		}
	}
	assertAuditRowsCarryNoKeyMaterial(t, f.DB, seededRawKey, key.KeyHash)
}

// ─── AdminToggleApiKey ──────────────────────────────────────────────────────

func TestAdminToggleApiKey_RecordsInternalKeyToggledAudit(t *testing.T) {
	f := setupInternalKeyAuditFixture(t)
	key, seededRawKey := seedAuditTestInternalApiKey(t, f.DB, "toggle-target")

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
	// The row must say which way the toggle went, and agree with the table:
	// the seed is enabled, so one toggle disables.
	var after repo.InternalApiKey
	if err := f.DB.First(&after, key.Id).Error; err != nil {
		t.Fatalf("re-read key: %v", err)
	}
	if after.Enabled {
		t.Fatalf("toggle left the seeded (enabled) key enabled")
	}
	if !strings.Contains(events[0].Details, `"enabled":false`) {
		t.Errorf("internal_key.toggled Details = %s, want it to carry the resulting state \"enabled\":false", events[0].Details)
	}
	assertAuditRowsCarryNoKeyMaterial(t, f.DB, seededRawKey, key.KeyHash)
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
