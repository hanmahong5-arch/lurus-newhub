package router

// metrics_instance_test.go — real-router lock for X-Lurus-Instance and the
// lurus_gateway_instance_info series: SetRouter must both mount the header
// middleware after metricsAuthMiddleware (never before it — an unauthorized
// scrape must not leak which pod answered) and publish the info gauge before
// the /metrics route is reachable.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/web"

	"github.com/gin-gonic/gin"
)

func TestSetRouter_MetricsCarriesInstanceHeaderAndInfoSeries(t *testing.T) {
	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = prevMaster })
	t.Setenv("FRONTEND_BASE_URL", "")
	t.Setenv("POD_NAME", "test-pod-a")
	t.Setenv("POD_NAMESPACE", "lurus-newhub-uat")

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:1234" // private, no forwarding header -> default-allow path
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from a direct in-cluster scrape, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Lurus-Instance"); got != "test-pod-a" {
		t.Errorf("X-Lurus-Instance = %q, want test-pod-a", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, `lurus_gateway_instance_info{`) {
		t.Fatal("response body does not contain the lurus_gateway_instance_info series")
	}
	if !strings.Contains(body, `pod="test-pod-a"`) {
		t.Errorf("lurus_gateway_instance_info series does not carry pod=%q, body snippet around it:\n%s", "test-pod-a", grepAround(body, "lurus_gateway_instance_info"))
	}
	if !strings.Contains(body, `namespace="lurus-newhub-uat"`) {
		t.Errorf("lurus_gateway_instance_info series does not carry namespace=%q, body snippet around it:\n%s", "lurus-newhub-uat", grepAround(body, "lurus_gateway_instance_info"))
	}
	if !strings.Contains(body, `version="`) {
		t.Errorf("lurus_gateway_instance_info series carries no version label, body snippet around it:\n%s", grepAround(body, "lurus_gateway_instance_info"))
	}
	if strings.Contains(body, `version=""`) {
		t.Errorf("lurus_gateway_instance_info series carries an empty version label, body snippet around it:\n%s", grepAround(body, "lurus_gateway_instance_info"))
	}
}

// TestSetRouter_MetricsRejectedScrapeLeaksNoInstance locks L3 finding #4: a
// scrape carrying a forwarding header (relayed by nginx, RemoteAddr no
// longer trustworthy) with no METRICS_AUTH_TOKEN configured must be
// rejected by metricsAuthMiddleware, and instanceHeader — mounted after the
// auth gate specifically so a rejected scrape never leaks which pod
// answered — must never have run.
func TestSetRouter_MetricsRejectedScrapeLeaksNoInstance(t *testing.T) {
	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = prevMaster })
	t.Setenv("FRONTEND_BASE_URL", "")
	t.Setenv("POD_NAME", "test-pod-a")
	t.Setenv("METRICS_AUTH_TOKEN", "") // unset: the forwarded-header path must fail closed

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:1234"                // private RemoteAddr, but...
	req.Header.Set("X-Forwarded-For", "203.0.113.5") // ...a forwarding header is present
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a forwarded scrape with no METRICS_AUTH_TOKEN, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Lurus-Instance"); got != "" {
		t.Errorf("X-Lurus-Instance = %q on a rejected scrape, want absent (instanceHeader must not run before the auth gate)", got)
	}
}

// grepAround returns a small window of text around the first occurrence of
// needle, for a more useful assertion failure message on a very large body.
func grepAround(body, needle string) string {
	i := strings.Index(body, needle)
	if i < 0 {
		return "(not found)"
	}
	end := i + 200
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
