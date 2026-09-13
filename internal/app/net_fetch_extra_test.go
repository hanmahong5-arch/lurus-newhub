package app

// net_fetch_extra_test.go — coverage for the HTTP fetch helpers (download,
// file-type sniffing, image/file base64 fetch) and the graceful HTTP response
// utilities. Real httptest servers exercise the success + status-error paths;
// SSRF protection is relaxed so loopback targets are reachable, and the
// download-size cap is raised above 0 (the env default that would reject
// everything).

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// raiseDownloadCap sets the download-size cap to 10MB for the test and restores
// it afterwards (env default is 0 = reject-all).
func raiseDownloadCap(t *testing.T) {
	t.Helper()
	prev := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 10
	t.Cleanup(func() { constant.MaxFileDownloadMB = prev })
}

func pngServer(t *testing.T, contentType string) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{G: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	body := buf.Bytes()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
}

// ─── download.go ──────────────────────────────────────────────────────────

func TestDoDownloadRequest_Success(t *testing.T) {
	allowLocalFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	resp, err := DoDownloadRequest(srv.URL, "unit-test")
	if err != nil {
		t.Fatalf("DoDownloadRequest: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDoDownloadRequest_SSRFReject(t *testing.T) {
	// SSRF on + no private IPs => loopback rejected before any request.
	fs := system_setting.GetFetchSetting()
	prevSSRF, prevPriv := fs.EnableSSRFProtection, fs.AllowPrivateIp
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	t.Cleanup(func() { fs.EnableSSRFProtection, fs.AllowPrivateIp = prevSSRF, prevPriv })

	resp, err := DoDownloadRequest("http://127.0.0.1:1/x")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected SSRF rejection for loopback download")
	}
}

func TestDoWorkerRequest_NotEnabled(t *testing.T) {
	// Worker mode is off by default => immediate error.
	resp, err := DoWorkerRequest(&WorkerRequest{URL: "https://example.com"})
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected error when worker mode is disabled")
	}
}

func TestDoWorkerRequest_HTTPTargetRejectedWhenNotAllowed(t *testing.T) {
	// Worker enabled but WorkerAllowHttpImageRequestEnabled left false => an
	// http:// (non-https) target url is rejected before dialing.
	prevURL := system_setting.WorkerUrl
	prevAllow := system_setting.WorkerAllowHttpImageRequestEnabled
	system_setting.WorkerUrl = "https://worker.example.com"
	system_setting.WorkerAllowHttpImageRequestEnabled = false
	t.Cleanup(func() {
		system_setting.WorkerUrl = prevURL
		system_setting.WorkerAllowHttpImageRequestEnabled = prevAllow
	})

	resp, err := DoWorkerRequest(&WorkerRequest{URL: "http://insecure.example.com/x"})
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected 'only support https url' rejection")
	}
}

// ─── file_decoder.go ──────────────────────────────────────────────────────

func TestGetFileTypeFromUrl_ContentTypeHeader(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png; charset=binary")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	got, err := GetFileTypeFromUrl(c, srv.URL)
	if err != nil {
		t.Fatalf("GetFileTypeFromUrl: %v", err)
	}
	if got != "image/png" {
		t.Errorf("mime = %q, want image/png (params stripped)", got)
	}
}

func TestGetFileTypeFromUrl_ContentDispositionFilename(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// octet-stream forces the code past the Content-Type shortcut into the
		// Content-Disposition filename-extension branch.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	got, err := GetFileTypeFromUrl(c, srv.URL)
	if err != nil {
		t.Fatalf("GetFileTypeFromUrl: %v", err)
	}
	if got != "application/pdf" {
		t.Errorf("mime = %q, want application/pdf (from filename ext)", got)
	}
}

func TestGetFileTypeFromUrl_URLExtensionFallback(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// octet-stream + no disposition => fall back to the URL path extension.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	got, err := GetFileTypeFromUrl(c, srv.URL+"/assets/photo.png?v=2")
	if err != nil {
		t.Fatalf("GetFileTypeFromUrl: %v", err)
	}
	if got != "image/png" {
		t.Errorf("mime = %q, want image/png (from url extension)", got)
	}
}

func TestGetFileBase64FromUrl_OctetStreamGuessesFromExt(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	fd, err := GetFileBase64FromUrl(c, srv.URL+"/clip.mp3")
	if err != nil {
		t.Fatalf("GetFileBase64FromUrl: %v", err)
	}
	if fd.MimeType != "audio/mp3" {
		t.Errorf("mime = %q, want audio/mp3 (guessed from url ext)", fd.MimeType)
	}
}

func TestGetFileTypeFromUrl_ContentSniffFallback(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	// Build a real PNG body so the content-sniffing fallback (octet-stream +
	// no disposition + extensionless URL) can DetectContentType it as image/png.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	body := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := createTestGinContext()
	// Extensionless path forces the body-sniff branch.
	got, err := GetFileTypeFromUrl(c, srv.URL+"/download")
	if err != nil {
		t.Fatalf("GetFileTypeFromUrl: %v", err)
	}
	if got != "image/png" {
		t.Errorf("mime = %q, want image/png (content-sniffed)", got)
	}
}

func TestGetFileTypeFromUrl_Non200Errors(t *testing.T) {
	allowLocalFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := createTestGinContext()
	if _, err := GetFileTypeFromUrl(c, srv.URL); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestGetFileBase64FromUrl_ReturnsBase64(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("payload-bytes"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	fd, err := GetFileBase64FromUrl(c, srv.URL)
	if err != nil {
		t.Fatalf("GetFileBase64FromUrl: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(fd.Base64Data)
	if string(decoded) != "payload-bytes" {
		t.Errorf("decoded body = %q, want payload-bytes", decoded)
	}
}

func TestGetFileBase64FromUrl_CacheHit(t *testing.T) {
	// Pre-seed the per-request cache under the exact key the function computes,
	// so it returns the cached LocalFileData without any network fetch.
	c := createTestGinContext()
	const url = "https://cdn.example.com/a.png"
	cached := &types.LocalFileData{MimeType: "image/png", Base64Data: "Y2FjaGVk"}
	c.Set("file_download_"+common.GenerateHMAC(url), cached)

	fd, err := GetFileBase64FromUrl(c, url)
	if err != nil {
		t.Fatalf("GetFileBase64FromUrl cache: %v", err)
	}
	if fd != cached {
		t.Error("expected the cached LocalFileData to be returned unchanged")
	}
}

// ─── image.go ─────────────────────────────────────────────────────────────

func TestGetImageFromUrl_PNG(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := pngServer(t, "image/png")
	defer srv.Close()

	mime, data, err := GetImageFromUrl(srv.URL)
	if err != nil {
		t.Fatalf("GetImageFromUrl: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	if data == "" {
		t.Error("expected non-empty base64 image data")
	}
}

func TestGetImageFromUrl_RejectsOversized(t *testing.T) {
	allowLocalFetch(t)
	// Do NOT raise the download cap: MaxFileDownloadMB stays 0, so any non-empty
	// image body trips the size guard.
	srv := pngServer(t, "image/png")
	defer srv.Close()

	if _, _, err := GetImageFromUrl(srv.URL); err == nil {
		t.Fatal("expected size-limit rejection with zero download cap")
	}
}

func TestGetImageFromUrl_OctetStreamNonImageErrors(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	// octet-stream Content-Type but the body is not a decodable image, so the
	// sniff-decode step inside GetImageFromUrl fails and returns an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("definitely not an image payload"))
	}))
	defer srv.Close()

	if _, _, err := GetImageFromUrl(srv.URL); err == nil {
		t.Fatal("expected decode error for octet-stream non-image body")
	}
}

func TestGetImageFromUrl_RejectsNonImageContentType(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>"))
	}))
	defer srv.Close()

	if _, _, err := GetImageFromUrl(srv.URL); err == nil {
		t.Fatal("expected rejection for non-image content type")
	}
}

func TestGetImageFromUrl_OctetStreamSniffsFormat(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := pngServer(t, "application/octet-stream") // octet-stream => sniff branch
	defer srv.Close()

	mime, _, err := GetImageFromUrl(srv.URL)
	if err != nil {
		t.Fatalf("GetImageFromUrl: %v", err)
	}
	// octet-stream is resolved to the decoded format.
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png (sniffed from octet-stream)", mime)
	}
}

func TestDecodeUrlImageData_Non200Errors(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, _, err := DecodeUrlImageData(srv.URL); err == nil {
		t.Fatal("expected error on non-200 image response")
	}
}

func TestDecodeUrlImageData_RejectsNonImageType(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	if _, _, err := DecodeUrlImageData(srv.URL); err == nil {
		t.Fatal("expected error for non-image content type")
	}
}

func TestDecodeUrlImageData_PNG(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	srv := pngServer(t, "image/png")
	defer srv.Close()

	cfg, format, err := DecodeUrlImageData(srv.URL)
	if err != nil {
		t.Fatalf("DecodeUrlImageData: %v", err)
	}
	if format != "png" {
		t.Errorf("format = %q, want png", format)
	}
	if cfg.Width != 3 || cfg.Height != 2 {
		t.Errorf("config = %dx%d, want 3x2", cfg.Width, cfg.Height)
	}
}

func TestDecodeUrlImageData_LargeImageMultiRead(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)
	// A larger PNG (>8KB encoded) forces more than one read-limit iteration in
	// DecodeUrlImageData's progressive-decode loop.
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for x := 0; x < 256; x++ {
		for y := 0; y < 256; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	body := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cfg, format, err := DecodeUrlImageData(srv.URL)
	if err != nil {
		t.Fatalf("DecodeUrlImageData: %v", err)
	}
	if format != "png" || cfg.Width != 256 || cfg.Height != 256 {
		t.Errorf("decoded = %s %dx%d, want png 256x256", format, cfg.Width, cfg.Height)
	}
}

// ─── http.go ──────────────────────────────────────────────────────────────

func TestCloseResponseBodyGracefully_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on nil response: %v", r)
		}
	}()
	CloseResponseBodyGracefully(nil)
	CloseResponseBodyGracefully(&http.Response{})
}

func TestIOCopyBytesGracefully_WritesBodyAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	src := &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     http.Header{"X-Custom": {"v1"}, "Content-Length": {"999"}},
	}
	payload := []byte("copied-bytes")
	IOCopyBytesGracefully(c, src, payload)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if w.Body.String() != "copied-bytes" {
		t.Errorf("body = %q, want copied-bytes", w.Body.String())
	}
	if w.Header().Get("X-Custom") != "v1" {
		t.Errorf("X-Custom header not propagated: %q", w.Header().Get("X-Custom"))
	}
	// Content-Length must be recomputed from the payload, not copied from src.
	if got := w.Header().Get("Content-Length"); got != "12" {
		t.Errorf("Content-Length = %q, want 12 (len of payload)", got)
	}
}

// TestIOCopyBytesGracefully_GatewayRequestIdSurvivesVendorHeader locks the
// UpstreamHeadersNotForwarded skip: a vendor that sends its own value under
// the identically-named "X-Request-Id" header (the OpenAI-wire convention)
// must not overwrite the gateway's own id, already set on c.Writer before
// this runs (middleware.RequestId). The remaining names
// provider.upstreamRequestIdHeaders also captures must likewise not reach
// the client raw.
func TestIOCopyBytesGracefully_GatewayRequestIdSurvivesVendorHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Mirrors middleware.RequestId's c.Header calls, which run before any
	// provider handler (and therefore before IOCopyBytesGracefully) does.
	c.Header("X-Request-Id", "gateway-minted-id")
	c.Header("X-Oneapi-Request-Id", "gateway-minted-id")

	src := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Request-Id":        {"vendor-id"},
			"X-Oneapi-Request-Id": {"vendor-id"},
			"Request-Id":          {"vendor-anthropic-id"},
			"Openai-Request-Id":   {"vendor-relay-id"},
			"Cf-Ray":              {"vendor-cf-ray"},
			"X-Custom":            {"kept"},
		},
	}
	IOCopyBytesGracefully(c, src, []byte("body"))

	if got := w.Header().Get("X-Request-Id"); got != "gateway-minted-id" {
		t.Errorf("X-Request-Id = %q, want the gateway's own id (vendor's must not overwrite it)", got)
	}
	if got := w.Header().Get("X-Oneapi-Request-Id"); got != "gateway-minted-id" {
		t.Errorf("X-Oneapi-Request-Id = %q, want the gateway's own id", got)
	}
	if got := w.Header().Get("Request-Id"); got != "" {
		t.Errorf("Request-Id = %q, want empty (vendor's own id header must not reach the client raw)", got)
	}
	if got := w.Header().Get("Openai-Request-Id"); got != "" {
		t.Errorf("Openai-Request-Id = %q, want empty", got)
	}
	if got := w.Header().Get("Cf-Ray"); got != "" {
		t.Errorf("Cf-Ray = %q, want empty", got)
	}
	// An unrelated header must still be copied through unaffected.
	if got := w.Header().Get("X-Custom"); got != "kept" {
		t.Errorf("X-Custom = %q, want kept (unrelated headers still copy)", got)
	}
}
