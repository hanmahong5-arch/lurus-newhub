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
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var modelsWriteTestDBCounter atomic.Int64

type modelsWriteTestCtx struct {
	router     *gin.Engine
	db         *gorm.DB
	tenantSlug string
	cleanup    func()
}

func setupModelsWriteRouter(t *testing.T) *modelsWriteTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := modelsWriteTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:v2modelswrite%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{}, &repo.Tenant{}, &repo.Model{}, &repo.Vendor{}, &repo.Option{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
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

	// Seed tenant.
	tenant := &repo.Tenant{
		Id:           fmt.Sprintf("tenant-mw-%d", n),
		Slug:         "acme",
		Name:         "Acme Corp",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: fmt.Sprintf("org_mw_%d", n),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Seed user.
	user := &repo.User{
		Username: "tester", DisplayName: "Tester",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "tester@test.local",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	router := gin.New()
	mockAuth := func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Next()
	}
	router.POST("/api/v2/:tenant_slug/models", mockAuth, CreateModelV2)
	router.DELETE("/api/v2/:tenant_slug/models/:id", mockAuth, DeleteModelV2)

	ctx := &modelsWriteTestCtx{
		router:     router,
		db:         db,
		tenantSlug: "acme",
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

func postModel(ctx *modelsWriteTestCtx, body interface{}) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/"+ctx.tenantSlug+"/models", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func deleteModel(ctx *modelsWriteTestCtx, id interface{}) *httptest.ResponseRecorder {
	url := fmt.Sprintf("/api/v2/%s/models/%v", ctx.tenantSlug, id)
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parseWriteResp(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// 1. Create happy path.
func TestV2ModelsWrite_CreateHappy(t *testing.T) {
	ctx := setupModelsWriteRouter(t)

	w := postModel(ctx, map[string]interface{}{
		"model_name": "gpt-4o",
		"vendor":     "OpenAI",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out["success"])
	}
	data := out["data"].(map[string]interface{})
	if data["model_name"] != "gpt-4o" {
		t.Errorf("expected model_name=gpt-4o, got %v", data["model_name"])
	}
	// created_time must be a number.
	if _, ok := data["created_time"]; !ok {
		t.Error("expected created_time field in response data")
	}
}

// 2. Tenant 404.
func TestV2ModelsWrite_TenantNotFound(t *testing.T) {
	ctx := setupModelsWriteRouter(t)

	// Use a slug that doesn't exist.
	url := "/api/v2/nonexistent-slug/models"
	data, _ := json.Marshal(map[string]interface{}{"model_name": "gpt-4o"})
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown tenant, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("expected error_code TENANT_NOT_FOUND, got %v", out["error_code"])
	}
}

// 3. Duplicate model_name → 409.
func TestV2ModelsWrite_DuplicateName409(t *testing.T) {
	ctx := setupModelsWriteRouter(t)

	// Create once — should succeed.
	w := postModel(ctx, map[string]interface{}{"model_name": "dup-model"})
	if w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d — body: %s", w.Code, w.Body.String())
	}

	// Create again with same name — must be 409.
	w2 := postModel(ctx, map[string]interface{}{"model_name": "dup-model"})
	if w2.Code != http.StatusConflict {
		t.Fatalf("duplicate create: expected 409, got %d — body: %s", w2.Code, w2.Body.String())
	}
	out := parseWriteResp(t, w2)
	if out["error_code"] != "MODEL_NAME_CONFLICT" {
		t.Errorf("expected error_code MODEL_NAME_CONFLICT, got %v", out["error_code"])
	}
}

// 4. Delete happy path.
func TestV2ModelsWrite_DeleteHappy(t *testing.T) {
	ctx := setupModelsWriteRouter(t)

	// Seed a model directly.
	m := &repo.Model{ModelName: "to-delete", Status: 1}
	if err := ctx.db.Create(m).Error; err != nil {
		t.Fatalf("seed model: %v", err)
	}

	w := deleteModel(ctx, m.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out["success"])
	}

	// Verify soft-delete: record not found via normal query.
	var found repo.Model
	err := ctx.db.First(&found, m.Id).Error
	if err == nil {
		t.Error("model should not be findable after soft-delete via normal First()")
	}
}

// 5. Delete non-existent → 404.
func TestV2ModelsWrite_DeleteNotFound(t *testing.T) {
	ctx := setupModelsWriteRouter(t)

	w := deleteModel(ctx, 99999)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent model, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["error_code"] != "MODEL_NOT_FOUND" {
		t.Errorf("expected error_code MODEL_NOT_FOUND, got %v", out["error_code"])
	}
}
