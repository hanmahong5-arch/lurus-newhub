package router

// trusted_proxies_test.go — lock for SEC-1: without gin.SetTrustedProxies,
// gin trusts every proxy in the chain and lets ClientIP() read the
// left-most, fully attacker-controlled hop of X-Forwarded-For. Production
// sits behind host nginx on a single NodePort with no k8s Ingress, so the
// only hop actually between the client and gin is that nginx; anything
// beyond it in the header is unverified client input. ClientIP() feeds the
// IP-keyed rate limiters (middleware/rate-limit.go), the token IP allow-list
// (middleware/auth.go), governance audit logging and log persistence — an
// unconfigured trust boundary lets a caller forge a fresh identity per
// request and dodge every one of those by IP.
//
// Verified by mutation: commenting out the engine.SetTrustedProxies(cidrs)
// call inside ConfigureTrustedProxies (leaving the SysLog line intact)
// makes all three cases below fail — cases 1 and 2
// both echo the spoofed leftmost XFF value (203.0.113.9) instead of the real
// hop, and case 3's second request gets 200 instead of 429 because the two
// requests hash to different rate-limit bucket keys.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func trustedProxiesTestDefaults() []string {
	return []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fe80::/10",
	}
}

func TestTrustedProxies_SpoofedXFFIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Case 1: request arrives from a trusted (RFC1918) hop, e.g. the host
	// nginx reverse proxy. Gin should trust the XFF chain it appended to and
	// resolve ClientIP() to the right-most-trusted / left-most-untrusted
	// entry: 198.51.100.7, not the attacker-supplied 203.0.113.9.
	t.Run("trusted hop uses rightmost XFF entry", func(t *testing.T) {
		engine := gin.New()
		if err := ConfigureTrustedProxies(engine, trustedProxiesTestDefaults()); err != nil {
			t.Fatalf("ConfigureTrustedProxies() error = %v", err)
		}
		engine.GET("/ip", func(c *gin.Context) {
			c.String(http.StatusOK, c.ClientIP())
		})

		req := httptest.NewRequest(http.MethodGet, "/ip", nil)
		req.RemoteAddr = "10.42.0.1:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.7")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)

		if got := rec.Body.String(); got != "198.51.100.7" {
			t.Errorf("ClientIP() = %q, want %q", got, "198.51.100.7")
		}
	})

	// Case 2: request arrives from an UNtrusted hop. Gin must ignore the XFF
	// header entirely and fall back to RemoteAddr, so a direct-to-pod caller
	// (bypassing nginx) cannot forge any identity via the header.
	t.Run("untrusted hop falls back to RemoteAddr", func(t *testing.T) {
		engine := gin.New()
		if err := ConfigureTrustedProxies(engine, trustedProxiesTestDefaults()); err != nil {
			t.Fatalf("ConfigureTrustedProxies() error = %v", err)
		}
		engine.GET("/ip", func(c *gin.Context) {
			c.String(http.StatusOK, c.ClientIP())
		})

		req := httptest.NewRequest(http.MethodGet, "/ip", nil)
		req.RemoteAddr = "203.0.113.50:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.7")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)

		if got := rec.Body.String(); got != "203.0.113.50" {
			t.Errorf("ClientIP() = %q, want %q", got, "203.0.113.50")
		}
	})

	// Case 3: end-to-end proof that the fix actually closes the rate-limit
	// bypass, not just that ClientIP() reports differently in isolation. An
	// attacker behind the trusted nginx hop who varies the leftmost XFF
	// entry per request must still land in ONE bucket and get 429 on the
	// second request, not a fresh bucket every time.
	t.Run("IP-keyed limiter is not bypassable via spoofed XFF", func(t *testing.T) {
		prevEnable, prevNum, prevDur := common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration
		prevRedis := common.RedisEnabled
		common.GlobalWebRateLimitEnable = true
		common.GlobalWebRateLimitNum = 1
		common.GlobalWebRateLimitDuration = 180
		common.RedisEnabled = false
		t.Cleanup(func() {
			common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration = prevEnable, prevNum, prevDur
			common.RedisEnabled = prevRedis
		})

		engine := gin.New()
		if err := ConfigureTrustedProxies(engine, trustedProxiesTestDefaults()); err != nil {
			t.Fatalf("ConfigureTrustedProxies() error = %v", err)
		}
		engine.GET("/limited", middleware.GlobalWebRateLimit(), func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		remoteAddr := "10.42.0.9:5555"
		for i, spoofed := range []string{"203.0.113.9", "203.0.113.10"} {
			req := httptest.NewRequest(http.MethodGet, "/limited", nil)
			req.RemoteAddr = remoteAddr
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("%s, 198.51.100.7", spoofed))
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)

			if i == 0 {
				if rec.Code != http.StatusOK {
					t.Fatalf("first request status = %d, want %d", rec.Code, http.StatusOK)
				}
				continue
			}
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("second request (spoofed XFF varied) status = %d, want %d (bucket bypassed via forged XFF)", rec.Code, http.StatusTooManyRequests)
			}
		}
	})
}
