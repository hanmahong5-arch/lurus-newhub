package handler

// task_media_guard_test.go — direct unit coverage for task_media_guard.go's
// pure helpers (parseDataURL, allowedArtifactScheme, hostnameOnly,
// copyCapped). The two guard functions that gate live requests
// (isSelfOrLoopURL, and the scheme allow-list as consulted from
// streamMediaContent/VideoProxy) are exercised through the real handlers in
// task_artifacts_test.go and video_proxy_test.go instead — those are the
// REAL-CHAIN-RULE-relevant tests; this file is the narrower unit layer
// underneath them.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func TestParseDataURL_Base64(t *testing.T) {
	mimeType, data, ok := parseDataURL("data:text/plain;base64,aGVsbG8=") // "hello"
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if mimeType != "text/plain" {
		t.Errorf("mimeType = %q, want text/plain", mimeType)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want hello", string(data))
	}
}

func TestParseDataURL_PlainNotBase64(t *testing.T) {
	mimeType, data, ok := parseDataURL("data:text/plain,hello%20world")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if mimeType != "text/plain" {
		t.Errorf("mimeType = %q, want text/plain", mimeType)
	}
	if string(data) != "hello world" {
		t.Errorf("data = %q, want %q (percent-decoded)", string(data), "hello world")
	}
}

// TestParseDataURL_PlainNotBase64PreservesLiteralPlus is A-F7's oracle: RFC
// 2397 payloads are percent-encoded octet-by-octet, so a literal '+' in the
// payload must stay '+', not become a space the way
// application/x-www-form-urlencoded query-string decoding (url.QueryUnescape
// — the previous implementation) would turn it into.
func TestParseDataURL_PlainNotBase64PreservesLiteralPlus(t *testing.T) {
	mimeType, data, ok := parseDataURL("data:text/plain,a+b%20c")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if mimeType != "text/plain" {
		t.Errorf("mimeType = %q, want text/plain", mimeType)
	}
	if string(data) != "a+b c" {
		t.Errorf("data = %q, want %q ('+' preserved literal, %%20 decoded to space)", string(data), "a+b c")
	}
}

func TestParseDataURL_DefaultMimeWhenOmitted(t *testing.T) {
	_, _, ok := parseDataURL("data:,plain-ascii")
	if !ok {
		t.Fatal("ok = false, want true")
	}
}

func TestParseDataURL_RejectsMalformedAndNonDataURLs(t *testing.T) {
	cases := []string{
		"http://example.com/x",   // not a data: URL at all
		"data:text/plain;base64", // missing comma
		"data:text/plain;base64,not-valid-base64!!!",
	}
	for _, raw := range cases {
		if _, _, ok := parseDataURL(raw); ok {
			t.Errorf("parseDataURL(%q) ok = true, want false", raw)
		}
	}
}

// TestAllowedArtifactScheme covers allowedArtifactScheme's own contract:
// http/https only. data: URLs never reach this function — parseDataURL
// intercepts them earlier in both streamMediaContent and VideoProxy's call
// sites — so "data" is deliberately in the refused list here, not the
// allowed one (cycle-8 L9 repair: the previous version of this test allowed
// "data", which was never true of the OUTBOUND-fetch scheme check this
// function actually gates).
func TestAllowedArtifactScheme(t *testing.T) {
	allowed := []string{"http", "https", "HTTP", "Https"}
	for _, s := range allowed {
		if !allowedArtifactScheme(s) {
			t.Errorf("allowedArtifactScheme(%q) = false, want true", s)
		}
	}
	refused := []string{"file", "ftp", "javascript", "data", ""}
	for _, s := range refused {
		if allowedArtifactScheme(s) {
			t.Errorf("allowedArtifactScheme(%q) = true, want false", s)
		}
	}
}

func TestHostnameOnly(t *testing.T) {
	cases := map[string]string{
		"example.com":      "example.com",
		"example.com:8080": "example.com",
		"127.0.0.1:3000":   "127.0.0.1",
		"":                 "",
	}
	for in, want := range cases {
		if got := hostnameOnly(in); got != want {
			t.Errorf("hostnameOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSelfOrLoopURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Request.Host = "hub.example.test:443"

	selfCases := []string{"http://hub.example.test/y", "http://hub.example.test:9999/y"}
	for _, raw := range selfCases {
		u := mustParseURLForTest(t, raw)
		if !isSelfOrLoopURL(c, u) {
			t.Errorf("isSelfOrLoopURL(%q) = false, want true (same host as inbound request)", raw)
		}
	}

	otherCases := []string{"http://vendor.example/y", "http://127.0.0.1:9999/y"}
	for _, raw := range otherCases {
		u := mustParseURLForTest(t, raw)
		if isSelfOrLoopURL(c, u) {
			t.Errorf("isSelfOrLoopURL(%q) = true, want false (different host from inbound request)", raw)
		}
	}
}

func mustParseURLForTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestCopyCapped_UnderCapCopiesEverything(t *testing.T) {
	src := bytes.NewBufferString("short payload")
	var dst bytes.Buffer
	n, err := copyCapped(&dst, src, 1024)
	if err != nil {
		t.Fatalf("copyCapped error: %v", err)
	}
	if n != int64(len("short payload")) || dst.String() != "short payload" {
		t.Errorf("n=%d dst=%q, want full payload copied with no error", n, dst.String())
	}
}

func TestCopyCapped_OverCapTruncates(t *testing.T) {
	src := bytes.NewBufferString(strings.Repeat("x", 100))
	var dst bytes.Buffer
	n, err := copyCapped(&dst, src, 10)
	if err != nil {
		t.Fatalf("copyCapped error: %v", err)
	}
	if n != 10 || dst.Len() != 10 {
		t.Errorf("n=%d dst.Len()=%d, want both 10 (truncated at the cap)", n, dst.Len())
	}
}

// TestStreamMediaContent_RefusesNonHttpScheme locks the scheme refusal inside
// streamMediaContent. Today the listing only ever hands it http(s) or data
// URLs (artifactURLsForTask filters on the same prefixes), so the branch is
// not reachable through the route — which is exactly why it needs a direct
// test: without one, deleting the check leaves every package green and the
// next caller that passes an unfiltered URL gets a proxy that will fetch any
// scheme the HTTP client understands.
func TestStreamMediaContent_RefusesNonHttpScheme(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.invalid/x", "://malformed"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/t1/artifacts/k/content", nil)

		streamMediaContent(c, raw)

		if w.Code != http.StatusBadGateway {
			t.Errorf("streamMediaContent(%q) status = %d, want 502; body=%s", raw, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), string(types.ErrorCodeArtifactRequestRejected)) {
			t.Errorf("streamMediaContent(%q) body = %s, want the typed rejection code", raw, w.Body.String())
		}
	}
}
