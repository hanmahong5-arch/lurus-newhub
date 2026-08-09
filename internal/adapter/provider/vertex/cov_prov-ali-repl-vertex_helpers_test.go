package vertex

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// prov_ali_repl_vertex_nopCloser wraps a string body into an io.ReadCloser
// suitable for http.Response.Body in handler tests.
func prov_ali_repl_vertex_nopCloser(body string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(body))
}

// prov_ali_repl_vertex_newGinContext builds a minimal gin.Context suitable for
// exercising vertex adaptor code paths that only touch c.Request / c.Writer.
func prov_ali_repl_vertex_newGinContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, w
}
