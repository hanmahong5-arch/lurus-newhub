package ali

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// prov_ali_repl_vertex_nopCloser wraps a string body into an io.ReadCloser
// suitable for http.Response.Body in handler tests.
func prov_ali_repl_vertex_nopCloser(body string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(body))
}

func init() {
	gin.SetMode(gin.TestMode)
}

// prov_ali_repl_vertex_newGinContext builds a minimal gin.Context suitable for
// exercising ali adaptor code paths that only touch c.Request / c.Writer.
func prov_ali_repl_vertex_newGinContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, w
}

// prov_ali_repl_vertex_multipartRequest builds a gin.Context whose Request is a
// multipart/form-data POST carrying the given text fields and an "image" file
// field with the given bytes.
func prov_ali_repl_vertex_multipartRequest(t *testing.T, fields map[string]string, imageField string, imageBytes []byte) *gin.Context {
	t.Helper()
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s): %v", k, err)
		}
	}
	if imageBytes != nil {
		fw, err := mw.CreateFormFile(imageField, "test.png")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(imageBytes); err != nil {
			t.Fatalf("write image bytes: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("mw.Close: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	return c
}

// prov_ali_repl_vertex_pngBytes returns a tiny valid PNG so http.DetectContentType
// classifies it as image/png rather than falling back to octet-stream.
func prov_ali_repl_vertex_pngBytes() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
}

var prov_ali_repl_vertex_fixedStartTime = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
