package handler

// cov_h5e_missing_models_error_test.go — covers the one branch
// cov_handler-deep-c_missing_models_test.go's happy-path tests don't reach:
// GetMissingModels' DB-failure path, where repo.GetMissingModels errors out
// (models metadata table gone) after at least one enabled ability exists.
// Note the handler always answers 200 on this branch (success:false in the
// body, not an HTTP error status) — a deliberate API contract this test
// locks in rather than assumes.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

func h5eMissingModelsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/missing-models", GetMissingModels)
	return r
}

func TestGetMissingModels_ModelsTableMissing_Returns200WithSuccessFalse(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.Model{}); err != nil {
		t.Fatalf("automigrate models table: %v", err)
	}

	priority := int64(1)
	if err := ctx.DB.Create(&repo.Ability{
		Group: "default", Model: "some-model", ChannelId: 1, Enabled: true, Priority: &priority,
	}).Error; err != nil {
		t.Fatalf("seed enabled ability: %v", err)
	}

	if err := ctx.DB.Migrator().DropTable(&repo.Model{}); err != nil {
		t.Fatalf("drop models table: %v", err)
	}

	r := h5eMissingModelsRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing-models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (handler reports failure in body, not status), body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("success = %v, want false, body=%s", resp["success"], w.Body.String())
	}
	if _, ok := resp["message"]; !ok {
		t.Errorf("expected a 'message' field carrying the underlying error, got body=%s", w.Body.String())
	}
	if _, ok := resp["data"]; ok {
		t.Errorf("expected no 'data' field on the failure branch, got %v", resp["data"])
	}
}
