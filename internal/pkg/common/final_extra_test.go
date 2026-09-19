package common

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// buildOggPage builds one minimal OggS page carrying the given granule position.
func buildOggPage(granulePos uint64, dataLen int) []byte {
	buf := &bytes.Buffer{}
	buf.WriteString("OggS")
	buf.WriteByte(0)                                       // version
	buf.WriteByte(0)                                       // header type
	_ = binary.Write(buf, binary.LittleEndian, granulePos) // granule position
	_ = binary.Write(buf, binary.LittleEndian, uint32(1))  // serial
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))  // page sequence
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))  // checksum
	buf.WriteByte(1)                                       // number of segments
	buf.WriteByte(byte(dataLen))                           // segment table: one segment
	buf.Write(make([]byte, dataLen))                       // page payload
	return buf.Bytes()
}

// TestGetOpusDuration_CraftedOgg feeds a hand-built OggS page with a known
// granule position; Opus is fixed at 48kHz so granule/48000 = duration.
func TestGetOpusDuration_CraftedOgg(t *testing.T) {
	page := buildOggPage(48000, 10) // 48000 samples @ 48kHz = 1s
	d, err := GetAudioDuration(context.Background(), bytes.NewReader(page), ".opus")
	if err != nil {
		t.Fatalf("opus parse error: %v", err)
	}
	if d < 0.99 || d > 1.01 {
		t.Errorf("expected ~1.0s, got %f", d)
	}
}

func TestGetRequestBody_NoLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orig := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1 // unlimited branch
	t.Cleanup(func() { constant.MaxRequestBodyMB = orig })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("unbounded-body"))
	body, err := GetRequestBody(c)
	if err != nil || string(body) != "unbounded-body" {
		t.Fatalf("no-limit GetRequestBody = %q err=%v", body, err)
	}
}

func TestUnmarshalBodyReusable_MultipartJSONFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orig := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = orig })

	// Content-Type says multipart but carries no boundary → parseMultipartFormData
	// falls back to JSON decoding of the raw body.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"model":"gpt-4"}`))
	c.Request.Header.Set("Content-Type", "multipart/form-data")
	var dst struct {
		Model string `json:"model"`
	}
	if err := UnmarshalBodyReusable(c, &dst); err != nil {
		t.Fatalf("multipart JSON fallback error: %v", err)
	}
	if dst.Model != "gpt-4" {
		t.Errorf("fallback decode mismatch: %+v", dst)
	}
}

func TestValidateURL_DNSBranches(t *testing.T) {
	// ApplyIPFilterForDomain forces DNS resolution + per-IP checks.
	p := &SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false, // blacklist empty -> domain passes filter
		IpFilterMode:           false, // blacklist empty
		AllowedPorts:           []int{80, 443},
		ApplyIPFilterForDomain: true,
	}
	// localhost resolves to 127.0.0.1 (private) -> blocked after DNS.
	if err := p.ValidateURL("http://localhost"); err == nil {
		t.Error("localhost should be blocked as private after DNS resolution")
	}
	// A syntactically valid but unresolvable domain -> DNS failure error.
	if err := p.ValidateURL("http://nonexistent.invalid.example.doesnotexist"); err == nil {
		t.Error("unresolvable domain should surface a DNS error")
	}
}

func TestRefreshIdentityClientEnv(t *testing.T) {
	// RefreshIdentityClientEnv re-reads env into the package globals.
	t.Setenv("IDENTITY_SERVICE_INTERNAL_KEY", "refreshed-key")
	t.Setenv("IDENTITY_AUTH_REDIRECT", "true")
	origKey := IdentityServiceInternalKey
	origRedirect := IdentityAuthRedirect
	t.Cleanup(func() {
		IdentityServiceInternalKey = origKey
		IdentityAuthRedirect = origRedirect
	})
	RefreshIdentityClientEnv()
	if IdentityServiceInternalKey != "refreshed-key" || !IdentityAuthRedirect {
		t.Errorf("RefreshIdentityClientEnv did not apply env: key=%q redirect=%v",
			IdentityServiceInternalKey, IdentityAuthRedirect)
	}
}

func TestRateLimiter_ClearExpiredItems(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(50 * time.Millisecond) // starts the background sweeper
	// Without this the sweeper wakes every 50ms and takes this limiter's mutex
	// for the rest of the test binary — a goroutine the -race job's leak
	// accounting sees for every later test (cycle-12 L1 added Stop for exactly
	// this). l is local to this test, so stopping it affects nothing else.
	t.Cleanup(l.Stop)

	if !l.Request("sweep-key", 5, 60) {
		t.Fatal("first request should be allowed")
	}
	// Wait for the sweeper to run at least once and evict the stale bucket.
	time.Sleep(150 * time.Millisecond)

	// A subsequent request still succeeds (fresh or swept bucket).
	if !l.Request("sweep-key", 5, 60) {
		t.Error("request after sweep should be allowed")
	}
}
