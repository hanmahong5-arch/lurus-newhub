package handler

// v2_admin_audit_chain_test.go — hermetic coverage of the audit hash-chain
// verification endpoint (GET /api/v2/admin/audit/chain-verify). Self-contained
// router (pattern of audit_governance_test.go); trigger-level enforcement is
// PG-only and covered in internal/pkg/migration/audit_tamper_pg_test.go.

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var auditChainDBCounter atomic.Int64

type auditChainCtx struct {
	router  *gin.Engine
	db      *gorm.DB
	cleanup func()
}

func setupAuditChainRouter(t *testing.T) *auditChainCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := auditChainDBCounter.Add(1)
	dsn := fmt.Sprintf("file:auditChainH%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	prevDB := repo.DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false

	router := gin.New()
	// Route is root-gated in production (RootJWTAuth on /api/v2/admin);
	// hermetic tier exercises the handler behind a stub, like the other
	// admin audit tests.
	router.GET("/api/v2/admin/audit/chain-verify", VerifyAuditChainV2)

	return &auditChainCtx{
		router: router, db: db,
		cleanup: func() {
			repo.DB = prevDB
			common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		},
	}
}

func seedChainedEvent(t *testing.T, db *gorm.DB, action string) *entity.AuditEvent {
	t.Helper()
	e := &entity.AuditEvent{
		TenantID: "default", Timestamp: 1700000000, ActorType: "admin",
		ActorID: 1, Action: action, Resource: "token", ResourceID: 1,
	}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("seed chained event: %v", err)
	}
	if e.RowHash == "" {
		t.Fatalf("seed event %q was not chained (row_hash empty)", action)
	}
	return e
}

func TestVerifyAuditChainV2_CleanChain(t *testing.T) {
	ctx := setupAuditChainRouter(t)
	defer ctx.cleanup()

	seedChainedEvent(t, ctx.db, "a.one")
	seedChainedEvent(t, ctx.db, "a.two")
	seedChainedEvent(t, ctx.db, "a.three")
	// One legacy (pre-chain) row: reported, never failed.
	if err := ctx.db.Exec(`INSERT INTO audit_events
		(tenant_id, timestamp, actor_type, actor_id, action, resource, resource_id,
		 details, ip, request_id, retention_until, prev_hash, row_hash)
		VALUES ('default', 1690000000, 'user', 2, 'legacy.write', 'token', 0,
		 '', '', '', 0, '', '')`).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	w := doGET(ctx.router, "/api/v2/admin/audit/chain-verify")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	if body["success"] != true {
		t.Fatalf("success = %v, body = %s", body["success"], w.Body.String())
	}
	data, _ := body["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %s", w.Body.String())
	}
	if got := data["checked"].(float64); got != 3 {
		t.Errorf("checked = %v, want 3", got)
	}
	if got := data["legacy_rows"].(float64); got != 1 {
		t.Errorf("legacy_rows = %v, want 1", got)
	}
	if data["first_break"] != nil {
		t.Errorf("first_break = %v, want null", data["first_break"])
	}
	if got := data["link_breaks"].(float64); got != 0 {
		t.Errorf("link_breaks = %v, want 0", got)
	}
}

func TestVerifyAuditChainV2_ReportsTamper(t *testing.T) {
	ctx := setupAuditChainRouter(t)
	defer ctx.cleanup()

	seedChainedEvent(t, ctx.db, "a.one")
	victim := seedChainedEvent(t, ctx.db, "a.two")
	seedChainedEvent(t, ctx.db, "a.three")

	// Raw tamper (SQLite tier has no append-only trigger).
	if err := ctx.db.Exec("UPDATE audit_events SET actor_id = 666 WHERE id = ?", victim.ID).Error; err != nil {
		t.Fatalf("tamper: %v", err)
	}

	w := doGET(ctx.router, "/api/v2/admin/audit/chain-verify?tenant_id=default")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	data, _ := parseJSON(t, w)["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %s", w.Body.String())
	}
	fb, _ := data["first_break"].(map[string]interface{})
	if fb == nil {
		t.Fatalf("first_break = nil, want tampered row %d; body = %s", victim.ID, w.Body.String())
	}
	if got := fb["id"].(float64); int64(got) != victim.ID {
		t.Errorf("first_break.id = %v, want %d", got, victim.ID)
	}
	if fb["expected"] == fb["actual"] {
		t.Error("expected == actual on tampered row")
	}
}

func TestVerifyAuditChainV2_CursorPaging(t *testing.T) {
	ctx := setupAuditChainRouter(t)
	defer ctx.cleanup()

	for i := 0; i < 5; i++ {
		seedChainedEvent(t, ctx.db, fmt.Sprintf("a.%d", i))
	}

	w := doGET(ctx.router, "/api/v2/admin/audit/chain-verify?limit=2")
	data, _ := parseJSON(t, w)["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %s", w.Body.String())
	}
	if got := data["checked"].(float64); got != 2 {
		t.Errorf("checked = %v, want 2 (limit)", got)
	}
	next := int64(data["next_cursor"].(float64))
	if next == 0 {
		t.Fatal("next_cursor = 0, want a paging cursor")
	}

	w = doGET(ctx.router, fmt.Sprintf("/api/v2/admin/audit/chain-verify?limit=10&after_id=%d", next))
	data, _ = parseJSON(t, w)["data"].(map[string]interface{})
	if got := data["checked"].(float64); got != 3 {
		t.Errorf("second page checked = %v, want 3", got)
	}
	if data["first_break"] != nil || data["link_breaks"].(float64) != 0 {
		t.Errorf("cross-page linkage broke: %s", w.Body.String())
	}
}
