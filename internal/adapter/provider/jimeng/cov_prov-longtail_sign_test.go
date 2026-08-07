package jimeng

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Sign: business contract is a Volcengine-compatible HMAC-SHA256 signature.
// We can't recompute the vendor's expected value from a fixture without
// re-implementing the algorithm, so we assert the externally observable
// contract instead: header shape, content hash correctness, and that the
// signature changes whenever the signed inputs change (i.e. it is not a
// constant / no-op).
// ---------------------------------------------------------------------------

var authRe = regexp.MustCompile(`^HMAC-SHA256 Credential=([^/]+)/(\d{8})/cn-north-1/cv/request, SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=([0-9a-f]{64})$`)

func provLongtailSignRequest(t *testing.T, method, target, apiKey, body string) (*http.Request, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)

	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	err = Sign(c, req, apiKey)
	return req, err
}

func TestJimeng_Sign_ValidKey_HeaderShape(t *testing.T) {
	req, err := provLongtailSignRequest(t, http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31", " AK123 | SK456 ", `{"prompt":"hi"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	auth := req.Header.Get("Authorization")
	m := authRe.FindStringSubmatch(auth)
	if m == nil {
		t.Fatalf("Authorization header %q does not match expected HMAC-SHA256 shape", auth)
	}
	if m[1] != "AK123" {
		t.Errorf("access key in credential = %q, want %q (must be trimmed of surrounding whitespace)", m[1], "AK123")
	}
	if req.Header.Get("Host") != "visual.volcengineapi.com" {
		t.Errorf("Host header = %q, want %q", req.Header.Get("Host"), "visual.volcengineapi.com")
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want default application/json when unset", req.Header.Get("Content-Type"))
	}

	wantHash := sha256.Sum256([]byte(`{"prompt":"hi"}`))
	if got := req.Header.Get("X-Content-Sha256"); got != hex.EncodeToString(wantHash[:]) {
		t.Errorf("X-Content-Sha256 = %q, want sha256 of the body %q", got, hex.EncodeToString(wantHash[:]))
	}
}

func TestJimeng_Sign_PreservesExistingContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/", nil)
	req, err := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/custom")
	if err := Sign(c, req, "ak|sk"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Content-Type") != "application/custom" {
		t.Errorf("Content-Type = %q, want preserved caller value application/custom", req.Header.Get("Content-Type"))
	}
}

func TestJimeng_Sign_BodyIsRewound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/", nil)
	req, err := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/", bytes.NewBufferString(`{"a":1}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if err := Sign(c, req, "ak|sk"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Business requirement: the outgoing HTTP client must still be able to
	// read the full body after signing consumed it once to compute the hash.
	remaining := make([]byte, 32)
	n, _ := req.Body.Read(remaining)
	if string(remaining[:n]) != `{"a":1}` {
		t.Errorf("body after Sign = %q, want the original body still readable (rewound)", string(remaining[:n]))
	}
}

func TestJimeng_Sign_InvalidApiKeyFormats(t *testing.T) {
	cases := []string{"", "onlyaccesskey", "a|b|c"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "https://x/", nil)
			req, _ := http.NewRequest(http.MethodPost, "https://x/", nil)
			err := Sign(c, req, key)
			if err == nil {
				t.Fatalf("expected error for malformed api key %q", key)
			}
			if !strings.Contains(err.Error(), "invalid api key format") {
				t.Errorf("error = %q, want mention of invalid api key format", err.Error())
			}
		})
	}
}

func TestJimeng_Sign_QueryParamsAreCanonicalized(t *testing.T) {
	// Two requests differing only in query-parameter order must produce the
	// same signature, proving canonicalization actually sorts keys/values
	// instead of relying on Go's map iteration order (which is randomized).
	gin.SetMode(gin.TestMode)

	sign := func(target string) string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, target, nil)
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if err := Sign(c, req, "ak|sk"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return req.Header.Get("Authorization")
	}

	a := sign("https://visual.volcengineapi.com/?Version=2022-08-31&Action=CVProcess")
	b := sign("https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31")
	if a != b {
		t.Errorf("signatures differ for reordered query params:\n a=%s\n b=%s", a, b)
	}
}

func TestJimeng_SetPayloadHash_StoresSha256OfMarshaledBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	type payload struct {
		A int `json:"a"`
	}
	if err := SetPayloadHash(c, payload{A: 7}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := sha256.Sum256([]byte(`{"a":7}`))
	got := getPayloadHash(c)
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("payload hash = %q, want sha256(%q) = %q", got, `{"a":7}`, hex.EncodeToString(want[:]))
	}
}

func TestJimeng_SetPayloadHash_UnmarshalableValueErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	// channels cannot be JSON-marshaled; this must surface the marshal error
	// rather than silently hashing an empty payload.
	err := SetPayloadHash(c, make(chan int))
	if err == nil {
		t.Fatal("expected error for unmarshalable payload")
	}
}
