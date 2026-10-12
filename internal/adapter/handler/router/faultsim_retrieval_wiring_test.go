package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The rerank and embeddings wires are mounted behind the same FAULTSIM_TOKEN
// gate as the chat wire: present and token-protected when enabled, absent when
// the env is empty.
func TestFaultSimRetrievalRoutedAndGated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	paths := map[string]string{
		"/api/v2/faultsim/v1/rerank":     `{"model":"ok","query":"q","documents":["a","b"]}`,
		"/api/v2/faultsim/v1/embeddings": `{"model":"ok","input":"a"}`,
	}

	t.Run("enabled", func(t *testing.T) {
		t.Setenv("FAULTSIM_TOKEN", "test-token")
		engine := gin.New()
		SetApiV2Router(engine)
		for path, body := range paths {
			for _, tc := range []struct {
				token string
				want  int
			}{{"", http.StatusUnauthorized}, {"test-token", http.StatusOK}} {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if tc.token != "" {
					req.Header.Set("X-Faultsim-Token", tc.token)
				}
				engine.ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Errorf("%s token=%q: status=%d, want %d; body=%s", path, tc.token, w.Code, tc.want, w.Body.String())
				}
			}
		}
	})
	t.Run("absent by default", func(t *testing.T) {
		t.Setenv("FAULTSIM_TOKEN", "")
		for _, rt := range faultSimRoutes(t) {
			if _, ok := paths[rt.Path]; ok {
				t.Errorf("faultsim retrieval route registered with no FAULTSIM_TOKEN: %s", rt.Path)
			}
		}
	})
}
