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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Hermetic SQLite setup mirrors the pattern used in v2_pricing_test.go.

var pricingWriteTestDBCounter atomic.Int64

type pricingWriteCtx struct {
	router     *gin.Engine
	db         *gorm.DB
	tenantSlug string
	cleanup    func()
}

func setupPricingWriteRouter(t *testing.T) *pricingWriteCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:pricingwrite%d?mode=memory&cache=shared", pricingWriteTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Option{},
	} {
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
	// Bootstrap OptionMap so repo.UpdateOption does not panic on nil map.
	repo.InitOptionMap()

	slug := "acme-write"
	tenant := &repo.Tenant{
		Id:           "acme-write-id",
		Slug:         slug,
		Name:         "Acme Write Test",
		IDPOrgID: fmt.Sprintf("zitadel-write-%d", pricingWriteTestDBCounter.Load()),
		Status:       1,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	router := gin.New()
	mockAuth := func(c *gin.Context) { c.Next() }
	router.POST("/api/v2/:tenant_slug/pricing", mockAuth, UpdatePricingV2)

	ctx := &pricingWriteCtx{
		router:     router,
		db:         db,
		tenantSlug: slug,
	}
	ctx.cleanup = func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(ctx.cleanup)
	return ctx
}

func postPricing(ctx *pricingWriteCtx, slug string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/"+slug+"/pricing", &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parsePricingWrite(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// 1. HappyPath — valid batch → 200, updated_count == len(batch).
func TestV2PricingWrite_HappyPath(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	ratio := 1.5
	batch := []map[string]interface{}{
		{"model_name": "gpt-4o", "model_ratio": ratio},
		{"model_name": "claude-3.5-sonnet", "model_price": 3.0},
	}

	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	count, ok := data["updated_count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("updated_count = %v, want 2", data["updated_count"])
	}
}

// 2. TenantNotFound — unknown slug → 404 TENANT_NOT_FOUND.
func TestV2PricingWrite_TenantNotFound(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	batch := []map[string]interface{}{
		{"model_name": "gpt-4o", "model_ratio": 1.0},
	}

	w := postPricing(ctx, "no-such-tenant-xyz", batch)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", resp["error_code"])
	}
}

// 3. EmptyBatch — empty array body → 400 EMPTY_BATCH.
func TestV2PricingWrite_EmptyBatch(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	w := postPricing(ctx, ctx.tenantSlug, []interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "EMPTY_BATCH" {
		t.Errorf("error_code = %v, want EMPTY_BATCH", resp["error_code"])
	}
}

// 4. MissingModelName — item without model_name → 400 MISSING_MODEL_NAME.
func TestV2PricingWrite_MissingModelName(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	// model_name is deliberately absent.
	batch := []map[string]interface{}{
		{"model_ratio": 1.5},
	}

	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "MISSING_MODEL_NAME" {
		t.Errorf("error_code = %v, want MISSING_MODEL_NAME", resp["error_code"])
	}
}
