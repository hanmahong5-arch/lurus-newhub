/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ── test fixture ─────────────────────────────────────────────────────────────

var logExportTestDBCounter atomic.Int64

type logExportCtx struct {
	router      *gin.Engine
	db          *gorm.DB
	tenantID    string
	tenantSlug  string
	userID      int
	otherTenant *repo.Tenant
	cleanup     func()
}

func setupLogExportRouter(t *testing.T) *logExportCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:logexport%d?mode=memory&cache=shared", logExportTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{},
		&repo.Tenant{},
		&repo.Log{},
		&repo.Option{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	tenantID := "tenant-acme-export"
	tenantSlug := "acme-export"
	now := time.Now()
	if err := db.Create(&repo.Tenant{
		Id: tenantID, Name: "Acme Export", Slug: tenantSlug,
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_acme_export",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed main tenant: %v", err)
	}

	otherTenant := &repo.Tenant{
		Id: "tenant-other-export", Name: "Other", Slug: "other-export",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_other_export",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(otherTenant).Error; err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}

	user := &repo.User{
		Username: "export-tester", DisplayName: "Export Tester",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "export@test.local", TenantId: tenantID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Mock auth injects TenantContext. X-Mock-Tenant-ID overrides the default
	// so the TENANT_MISMATCH case can inject a different tenant context.
	mockAuth := func(c *gin.Context) {
		mockTID := c.GetHeader("X-Mock-Tenant-ID")
		if mockTID == "" {
			mockTID = tenantID
		}
		mockUID := user.Id
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID:      mockTID,
			UserID:        mockUID,
			IDPSubject: "zitadel_export_user",
			Email:         "export@test.local",
			Username:      "export-tester",
		})
		c.Set("id", mockUID)
		c.Next()
	}

	router := gin.New()
	router.GET("/api/v2/:tenant_slug/logs/export", mockAuth, ExportLogsV2)

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(cleanup)

	return &logExportCtx{
		router:      router,
		db:          db,
		tenantID:    tenantID,
		tenantSlug:  tenantSlug,
		userID:      user.Id,
		otherTenant: otherTenant,
		cleanup:     cleanup,
	}
}

func doExportGet(ctx *logExportCtx, slug, query, mockTenantID string) *httptest.ResponseRecorder {
	path := "/api/v2/" + slug + "/logs/export"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if mockTenantID != "" {
		req.Header.Set("X-Mock-Tenant-ID", mockTenantID)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func seedExportLog(t *testing.T, ctx *logExportCtx, modelName, tokenName string, createdAt int64) {
	t.Helper()
	l := &repo.Log{
		UserId:           ctx.userID,
		TenantId:         ctx.tenantID,
		Type:             repo.LogTypeConsume,
		ModelName:        modelName,
		TokenName:        tokenName,
		PromptTokens:     10,
		CompletionTokens: 20,
		Quota:            500,
		Ip:               "127.0.0.1",
		Content:          "test-content",
		CreatedAt:        createdAt,
	}
	if err := ctx.db.Create(l).Error; err != nil {
		t.Fatalf("seedExportLog: %v", err)
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestV2LogExport_HappyPath: seed 3 logs → GET export → valid CSV with header +
// 3 data rows, and Content-Disposition is set correctly.
func TestV2LogExport_HappyPath(t *testing.T) {
	ctx := setupLogExportRouter(t)

	base := int64(1_700_000_000)
	seedExportLog(t, ctx, "gpt-4o", "my-token", base)
	seedExportLog(t, ctx, "claude-3.5", "my-token", base+1)
	seedExportLog(t, ctx, "gemini-pro", "other-token", base+2)

	w := doExportGet(ctx, ctx.tenantSlug, "", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv prefix", ct)
	}

	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=") {
		t.Errorf("Content-Disposition = %q, missing attachment prefix", cd)
	}
	if !strings.Contains(cd, ctx.tenantSlug) {
		t.Errorf("Content-Disposition = %q, want slug %q in filename", cd, ctx.tenantSlug)
	}

	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}

	// First row must be the header.
	if len(records) < 1 {
		t.Fatal("CSV is empty, expected at least 1 row (header)")
	}
	wantHeader := csvHeader
	if len(records[0]) != len(wantHeader) {
		t.Errorf("header columns = %d, want %d", len(records[0]), len(wantHeader))
	}
	for i, col := range wantHeader {
		if records[0][i] != col {
			t.Errorf("header[%d] = %q, want %q", i, records[0][i], col)
		}
	}

	// Should have exactly 3 data rows.
	dataRows := records[1:]
	if len(dataRows) != 3 {
		t.Errorf("data rows = %d, want 3", len(dataRows))
	}
}

// TestV2LogExport_EmptyResult: tenant with no logs → only header row, status 200.
func TestV2LogExport_EmptyResult(t *testing.T) {
	ctx := setupLogExportRouter(t)

	// No logs seeded.
	w := doExportGet(ctx, ctx.tenantSlug, "", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}

	// Exactly 1 row: the header.
	if len(records) != 1 {
		t.Errorf("rows = %d, want 1 (header only), body:\n%s", len(records), w.Body.String())
	}
	if records[0][0] != "created_at" {
		t.Errorf("first header cell = %q, want created_at", records[0][0])
	}
}

// TestV2LogExport_TenantMismatch: authenticated for tenantID but URL slug points
// to the other tenant → 403 TENANT_MISMATCH.
func TestV2LogExport_TenantMismatch(t *testing.T) {
	ctx := setupLogExportRouter(t)

	// Inject the main tenant's ID into the auth context, but hit the other-tenant slug.
	w := doExportGet(ctx, ctx.otherTenant.Slug, "", ctx.tenantID)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
}

// TestV2LogExport_UnknownSlug: URL slug does not exist in the DB → 404.
func TestV2LogExport_UnknownSlug(t *testing.T) {
	ctx := setupLogExportRouter(t)

	w := doExportGet(ctx, "does-not-exist-ever", "", "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}
}

// TestV2LogExport_DefaultVirtualTenant: the "default" tenant is virtual
// (tenant id == slug == "default", no tenants row). Sibling v2 log endpoints
// accept it (they scope via the middleware tenant context only), so the
// export must too — regression: it returned 404 TENANT_NOT_FOUND.
func TestV2LogExport_DefaultVirtualTenant(t *testing.T) {
	ctx := setupLogExportRouter(t)

	// Seed a log under the virtual default tenant for the test user.
	l := &repo.Log{
		UserId:    ctx.userID,
		TenantId:  "default",
		Type:      repo.LogTypeConsume,
		ModelName: "gpt-4o",
		TokenName: "tk",
		Quota:     100,
		CreatedAt: 1_700_000_000,
	}
	if err := ctx.db.Create(l).Error; err != nil {
		t.Fatalf("seed default-tenant log: %v", err)
	}

	w := doExportGet(ctx, "default", "", "default")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("rows = %d, want 2 (header + 1 data row), body:\n%s", len(records), w.Body.String())
	}

	// Guard: slug "default" with a *real* tenant context must NOT bypass the
	// lookup — it still resolves (and 404s, since no "default" row exists).
	w2 := doExportGet(ctx, "default", "", ctx.tenantID)
	if w2.Code != http.StatusNotFound {
		t.Errorf("real-tenant context on /default: status = %d, want 404, body: %s", w2.Code, w2.Body.String())
	}
}

// TestV2LogExport_MaxRowsClamp: seed 10 logs, request max_rows=5 → exactly 5
// data rows returned. Verifies the cap is honoured.
func TestV2LogExport_MaxRowsClamp(t *testing.T) {
	ctx := setupLogExportRouter(t)

	base := int64(1_700_000_000)
	for i := 0; i < 10; i++ {
		seedExportLog(t, ctx, "gpt-4o", "tk", base+int64(i))
	}

	w := doExportGet(ctx, ctx.tenantSlug, "max_rows=5", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}

	// 1 header + 5 data rows.
	if len(records) != 6 {
		t.Errorf("rows = %d (incl header), want 6 (1 header + 5 data), body:\n%s", len(records), w.Body.String())
	}
}
