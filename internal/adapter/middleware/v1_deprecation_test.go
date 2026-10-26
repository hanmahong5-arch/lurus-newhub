package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestV1DeprecationHeaders_SetsOnLegacyAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(V1DeprecationHeaders())
	r.GET("/api/user/self", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	r.ServeHTTP(w, req)
	if w.Header().Get("Deprecation") != "true" {
		t.Fatalf("Deprecation=%q", w.Header().Get("Deprecation"))
	}
	if w.Header().Get("Sunset") == "" {
		t.Fatal("missing Sunset")
	}
}

func TestV1DeprecationHeaders_SkipsPublicAndV2(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(V1DeprecationHeaders())
	r.GET("/api/status", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/v2/~/user/me", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{"/api/status", "/api/v2/~/user/me"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)
		if w.Header().Get("Deprecation") != "" {
			t.Fatalf("%s got Deprecation=%q", path, w.Header().Get("Deprecation"))
		}
	}
}
