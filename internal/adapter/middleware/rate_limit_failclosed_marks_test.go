package middleware

// rate_limit_failclosed_marks_test.go — L4 (cycle 12). Cycle-11's operator
// decision D1 made redisRateLimiterKeyed fail OPEN on a Redis error, because
// failing closed (500) took the k8s probe paths behind GlobalAPIRateLimit
// out of readiness on every replica at once. Correct for traffic buckets —
// but it also means that while Redis is down nothing throttles
// redemption-code guessing, bootstrap user creation, TOTP backup-code
// regeneration or internal-API-key guessing.
//
// The fix is not "fail closed" (that is the outage D1 was about); it is
// "fall back to the process-local limiter" for the credential/abuse buckets
// only. A per-replica ceiling is weaker than the cluster-wide one and
// stronger than none.
//
// Two gates here:
//   - the behaviour, on both sides of the classification
//   - completeness: every mark this codebase hands to a limiter factory must
//     be classified, with a reason, rather than silently defaulting

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// rateLimitMarkSeq hands each armed test its own client IP: the in-memory
// limiter's buckets are package-global and outlive the test.
var rateLimitMarkSeq = 0

func nextRateLimitMarkIP() string {
	rateLimitMarkSeq++
	return "203.0.113." + strconv.Itoa(rateLimitMarkSeq%250+1)
}

// runFactoryTwice mounts rateLimitFactory(1, 60, mark) and issues two
// requests from one client IP, returning both status codes.
func runFactoryTwice(t *testing.T, mark string) (int, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mw := rateLimitFactory(1, 60, mark)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/x", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	ip := nextRateLimitMarkIP()
	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Forwarded-For", ip)
		req.RemoteAddr = ip + ":40000"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	return do(), do()
}

// TestRateLimitFactory_CredentialMarkFallsBackToMemory: with Redis dead, the
// redemption bucket still enforces its budget out of process memory.
func TestRateLimitFactory_CredentialMarkFallsBackToMemory(t *testing.T) {
	defer deadRedis(t)()

	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_backend_memory"))

	first, second := runFactoryTwice(t, "RD")
	if first != http.StatusOK {
		t.Fatalf("first request status = %d, want 200 (the budget is 1)", first)
	}
	if second != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429 — with Redis down the redemption bucket is not enforcing at all", second)
	}

	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_backend_memory"))
	if after < before+2 {
		t.Errorf("rate_limit_degraded_total{web_rate_limit_backend_memory} = %v, want >= %v — the fallback must stay visible on /metrics", after, before+2)
	}
}

// TestRateLimitFactory_TrafficMarkStillFailsOpen is the other half of the
// classification: the /api traffic bucket keeps cycle-11's fail-open, so a
// Redis blip cannot take the probe paths mounted behind it out of readiness.
func TestRateLimitFactory_TrafficMarkStillFailsOpen(t *testing.T) {
	defer deadRedis(t)()

	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_backend"))

	first, second := runFactoryTwice(t, "GA")
	if first != http.StatusOK || second != http.StatusOK {
		t.Fatalf("statuses = %d, %d; want 200, 200 (GlobalAPIRateLimit fails open on a Redis error — cycle-11 D1)", first, second)
	}

	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_backend"))
	if after < before+2 {
		t.Errorf("rate_limit_degraded_total{web_rate_limit_backend} = %v, want >= %v", after, before+2)
	}
}

// rateLimitMarksExemptFromMemoryFallback is the OTHER half of the
// classification: every mark that deliberately keeps cycle-11's fail-open,
// with the reason. The completeness gate below requires every mark in the
// codebase to appear either here or in rateLimitMemoryFallbackMarks.
var rateLimitMarksExemptFromMemoryFallback = map[string]string{
	"GW":  "GlobalWebRateLimit: SPA page loads. Throttling these out of per-replica memory during an outage would white-page the console (it already did once, 2026-08-30), and a page load is not a credential guess.",
	"GA":  "GlobalAPIRateLimit: carries the k8s probe paths (probeBypassPaths only exempts DIRECT in-cluster hits). Failing anything but open here is the readiness outage cycle-11 D1 fixed.",
	"GV":  "GlobalV2RateLimit: same traffic-bucket reasoning as GW/GA, for the /api/v2 console surface.",
	"DW":  "DownloadRateLimit: release-artifact download volume, not a credential.",
	"UP":  "UploadRateLimit: upload volume, not a credential.",
	"IKR": "InternalApiRateLimit read tier: keyed by an ALREADY AUTHENTICATED internal key, so it bounds capacity, not guessing. Platform's own calls run through it; a per-replica ceiling during a Redis outage would throttle the money path rather than an attacker.",
	"IKW": "InternalApiRateLimit write tier: same, already-authenticated key.",
	"IKP": "InternalApiRateLimit provisioning tier: same, already-authenticated key.",
	"IKF": "InternalApiRateLimit pool-fund tier: same, already-authenticated key.",
}

// TestRateLimitMarks_EveryMarkIsClassified enumerates the marks from source
// rather than from a hand-kept list: a new limiter added with a new mark
// fails this test until someone decides which side it is on.
func TestRateLimitMarks_EveryMarkIsClassified(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	sources := []struct {
		path string
		re   *regexp.Regexp
	}{
		{
			filepath.Join(root, "internal", "adapter", "middleware", "rate-limit.go"),
			regexp.MustCompile(`(?s)(?:rateLimitFactory|keyedRateLimitFactory)\([^)]*?"([^"]+)"`),
		},
		{
			filepath.Join(root, "internal", "adapter", "middleware", "rate-limit-v2.go"),
			regexp.MustCompile(`(?s)(?:rateLimitFactory|keyedRateLimitFactory)\([^)]*?"([^"]+)"`),
		},
		{
			filepath.Join(root, "internal", "adapter", "handler", "router", "internal-api-router.go"),
			regexp.MustCompile(`(?s)InternalApiRateLimit\([^)]*?"([^"]+)"`),
		},
	}

	found := map[string]string{}
	for _, src := range sources {
		raw, readErr := os.ReadFile(src.path)
		if readErr != nil {
			t.Fatalf("read %s: %v", src.path, readErr)
		}
		for _, m := range src.re.FindAllStringSubmatch(string(raw), -1) {
			found[m[1]] = filepath.Base(src.path)
		}
	}

	if len(found) == 0 {
		t.Fatal("found 0 rate-limit marks in source — the regexes or the paths are wrong, and this gate would pass on anything")
	}

	var unclassified []string
	for mark, file := range found {
		_, fallback := rateLimitMemoryFallbackMarks[mark]
		_, exempt := rateLimitMarksExemptFromMemoryFallback[mark]
		if fallback && exempt {
			t.Errorf("mark %q (%s) is in BOTH classification tables", mark, file)
		}
		if !fallback && !exempt {
			unclassified = append(unclassified, mark+" ("+file+")")
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("unclassified rate-limit marks: %v — add each to rateLimitMemoryFallbackMarks (credential/abuse bucket) or to rateLimitMarksExemptFromMemoryFallback with the reason", unclassified)
	}

	// The reverse direction: a classification entry for a mark that no
	// longer exists is stale documentation.
	for mark := range rateLimitMemoryFallbackMarks {
		if _, ok := found[mark]; !ok {
			t.Errorf("rateLimitMemoryFallbackMarks has %q but no limiter in source uses that mark", mark)
		}
	}
	for mark := range rateLimitMarksExemptFromMemoryFallback {
		if _, ok := found[mark]; !ok {
			t.Errorf("rateLimitMarksExemptFromMemoryFallback has %q but no limiter in source uses that mark", mark)
		}
	}
}

// TestRateLimitMemoryFallbackMarks_CarryReasons keeps the fallback table from
// degenerating into a bare list: each entry states which credential it is
// guarding, so a future reader can argue with the classification.
func TestRateLimitMemoryFallbackMarks_CarryReasons(t *testing.T) {
	if len(rateLimitMemoryFallbackMarks) == 0 {
		t.Fatal("rateLimitMemoryFallbackMarks is empty — nothing falls back and this lane's change is a no-op")
	}
	for mark, reason := range rateLimitMemoryFallbackMarks {
		if len(reason) < 40 {
			t.Errorf("mark %q reason is %q — too short to be a reason", mark, reason)
		}
	}
}
