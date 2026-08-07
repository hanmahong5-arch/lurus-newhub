package replicate

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// prov_ali_repl_vertex_initHTTPClient initializes the shared relay HTTP client
// (nil by default in a bare test binary) and allows loopback destinations so
// httptest.Server stubs are reachable, restoring settings on cleanup.
func prov_ali_repl_vertex_initHTTPClient(t *testing.T) {
	t.Helper()
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow })
}

func init() {
	gin.SetMode(gin.TestMode)
}

// prov_ali_repl_vertex_nopCloser wraps a string body into an io.ReadCloser
// suitable for http.Response.Body in handler tests.
func prov_ali_repl_vertex_nopCloser(body string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(body))
}

// prov_ali_repl_vertex_newGinContext builds a minimal gin.Context suitable for
// exercising replicate adaptor code paths that only touch c.Request / c.Writer.
func prov_ali_repl_vertex_newGinContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	return c, w
}

// prov_ali_repl_vertex_multipartRequest builds a gin.Context whose Request is a
// multipart/form-data POST carrying the given text fields and an optional file
// field (imageField == "" means no file is attached).
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
	if imageField != "" && imageBytes != nil {
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

func prov_ali_repl_vertex_pngBytes() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
}

// prov_ali_repl_vertex_uploadServer builds an httptest.Server that always
// answers a replicate /v1/files upload request with the given status/body,
// invoking onHit (if non-nil) once the request is received.
func prov_ali_repl_vertex_uploadServer(t *testing.T, onHit func(), body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onHit != nil {
			onHit()
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}
