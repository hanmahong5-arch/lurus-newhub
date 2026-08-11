package common

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// TestParseMultipartFormReusable_MissingBoundaryErrors covers the
// parseBoundary-error branch: a Content-Type claiming multipart without a
// boundary param must surface an error, not a nil form.
func TestParseMultipartFormReusable_MissingBoundaryErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("irrelevant"))
	c.Request.Header.Set("Content-Type", "multipart/form-data") // no boundary

	form, err := ParseMultipartFormReusable(c)
	if err == nil {
		t.Fatal("expected boundary-not-found error, got nil")
	}
	if form != nil {
		t.Errorf("expected nil form on error, got %+v", form)
	}
}

// TestParseMultipartFormReusable_MalformedBodyErrors covers the
// reader.ReadForm error branch: a well-formed Content-Type/boundary header
// paired with a body that doesn't actually contain that boundary must fail
// parsing rather than returning a bogus empty form.
func TestParseMultipartFormReusable_MalformedBodyErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// Declares boundary "real-boundary" but the body never contains it —
	// multipart.Reader.ReadForm must fail with io.ErrUnexpectedEOF-class error.
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("not actually multipart content"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=real-boundary")

	_, err := ParseMultipartFormReusable(c)
	if err == nil {
		t.Fatal("expected ReadForm error for a body missing its declared boundary, got nil")
	}
}

// TestProcessFormMap_MarshalErrorPropagates covers processFormMap's Marshal
// error branch: a form value that cannot be JSON-marshaled (a raw channel,
// smuggled in via the map[string]any contract) must return an error rather
// than a zero-valued destination struct with no error signal.
func TestProcessFormMap_MarshalErrorPropagates(t *testing.T) {
	formMap := map[string]any{"bad": make(chan int)}
	var dst struct {
		Bad string `json:"bad"`
	}
	if err := processFormMap(formMap, &dst); err == nil {
		t.Fatal("expected Marshal error for an unmarshalable form value, got nil")
	}
}

// TestProcessFormMap_HappyPath is the control case processFormMap's error
// test is diffed against.
func TestProcessFormMap_HappyPath(t *testing.T) {
	formMap := map[string]any{"name": "acme", "count": 3}
	var dst struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := processFormMap(formMap, &dst); err != nil {
		t.Fatalf("processFormMap: %v", err)
	}
	if dst.Name != "acme" || dst.Count != 3 {
		t.Errorf("unexpected decode: %+v", dst)
	}
}
