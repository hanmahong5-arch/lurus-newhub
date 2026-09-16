package app

// coverage_seam2_extra_test.go — HTTP-backed notify + file-download branches:
//   - sendBarkNotify success (non-worker) + SSRF reject,
//   - sendGotifyNotify success (priority clamp) + non-2xx error,
//   - GetFileTypeFromUrl / GetFileBase64FromUrl download-error and
//     size-exceeded branches, plus the DebugEnabled cache-hit log.
// All use httptest with private-IP fetch allowed (allowLocalFetch).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// ─── sendBarkNotify ────────────────────────────────────────────────────────

func TestSendBarkNotify_SuccessInterpolatesTemplate(t *testing.T) {
	allowLocalFetch(t)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// {{value}} in content is replaced by the Values slice; {{title}}/{{content}}
	// in the URL template are replaced by the (escaped) title/content.
	data := dto.NewNotify(dto.NotifyTypeQuotaExceed, "hello", "remaining "+dto.ContentValueParam, []interface{}{"$1.00"})
	err := sendBarkNotify(srv.URL+"/{{title}}/{{content}}", data)
	if err != nil {
		t.Fatalf("sendBarkNotify success: %v", err)
	}
	if !strings.Contains(gotPath, "hello") {
		t.Errorf("bark path %q did not contain the interpolated title", gotPath)
	}
}

func TestSendBarkNotify_SSRFRejectsPrivateIP(t *testing.T) {
	// Enable SSRF protection and disallow private IPs so the localhost URL is
	// rejected before any request is made.
	fs := system_setting.GetFetchSetting()
	prevSSRF, prevPriv := fs.EnableSSRFProtection, fs.AllowPrivateIp
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	t.Cleanup(func() { fs.EnableSSRFProtection, fs.AllowPrivateIp = prevSSRF, prevPriv })

	err := sendBarkNotify("http://127.0.0.1:9/push", dto.NewNotify("t", "s", "c", nil))
	if err == nil {
		t.Fatal("expected SSRF rejection for a private-IP bark URL")
	}
	if !strings.Contains(err.Error(), "reject") {
		t.Errorf("error = %v, want an SSRF 'reject' error", err)
	}
}

// ─── sendGotifyNotify ──────────────────────────────────────────────────────

func TestSendGotifyNotify_SuccessClampsPriority(t *testing.T) {
	allowLocalFetch(t)

	var gotToken string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// priority 99 is out of range → clamped to 5 inside sendGotifyNotify.
	err := sendGotifyNotify(srv.URL, "tok-123", 99, dto.NewNotify("gtitle", "gtitle", "gbody", nil))
	if err != nil {
		t.Fatalf("sendGotifyNotify success: %v", err)
	}
	if gotToken != "tok-123" {
		t.Errorf("gotify token = %q, want tok-123", gotToken)
	}
	if !strings.Contains(string(gotBody), "gbody") {
		t.Errorf("gotify body %q missing message", string(gotBody))
	}
}

func TestSendGotifyNotify_Non2xxErrors(t *testing.T) {
	allowLocalFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := sendGotifyNotify(srv.URL, "tok", 5, dto.NewNotify("t", "s", "c", nil))
	if err == nil {
		t.Fatal("expected error on 500 gotify response")
	}
}

// ─── file_decoder download-error + size-limit ──────────────────────────────

func TestGetFileTypeFromUrl_DownloadErrorReturnsErr(t *testing.T) {
	allowLocalFetch(t)
	c := createTestGinContext()
	// Port 9 (discard) refuses connections quickly → transport error.
	if _, err := GetFileTypeFromUrl(c, "http://127.0.0.1:9/x.png"); err == nil {
		t.Fatal("expected a download error for an unreachable host")
	}
}

func TestGetFileBase64FromUrl_DownloadErrorReturnsErr(t *testing.T) {
	allowLocalFetch(t)
	c := createTestGinContext()
	if _, err := GetFileBase64FromUrl(c, "http://127.0.0.1:9/x.bin"); err == nil {
		t.Fatal("expected a download error for an unreachable host")
	}
}

func TestGetFileBase64FromUrl_SizeExceededErrors(t *testing.T) {
	allowLocalFetch(t)
	// Force the max download size to 0 so any non-empty body trips the guard.
	prev := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 0
	t.Cleanup(func() { constant.MaxFileDownloadMB = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("some payload bytes"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	_, err := GetFileBase64FromUrl(c, srv.URL+"/big.bin")
	if err == nil {
		t.Fatal("expected a size-exceeded error")
	}
	if !strings.Contains(err.Error(), "size exceeds") {
		t.Errorf("error = %v, want a size-exceeded error", err)
	}
}

func TestGetFileBase64FromUrl_CacheHitWithDebug(t *testing.T) {
	allowLocalFetch(t)
	prevDebug := common.DebugEnabled
	common.DebugEnabled = true
	prevMax := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	t.Cleanup(func() {
		common.DebugEnabled = prevDebug
		constant.MaxFileDownloadMB = prevMax
	})

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	url := srv.URL + "/pic.png"
	first, err := GetFileBase64FromUrl(c, url)
	if err != nil {
		t.Fatalf("first download: %v", err)
	}
	// Second call must hit the per-request context cache (DebugEnabled log path)
	// and NOT re-download.
	second, err := GetFileBase64FromUrl(c, url)
	if err != nil {
		t.Fatalf("second download: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want 1 (second served from cache)", hits)
	}
	if first != second {
		t.Errorf("cache hit returned a different pointer/value")
	}
}
