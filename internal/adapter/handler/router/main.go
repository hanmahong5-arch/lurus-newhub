package router

import (
	"crypto/subtle"
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, buildFS embed.FS, indexPage []byte) {
	// Add OpenTelemetry tracing middleware (must be first to capture full request)
	router.Use(tracing.Middleware())

	// Add Prometheus metrics middleware
	router.Use(metrics.Middleware())

	// Hardening response headers on every response. Must be registered here,
	// before any router.Group()/GET()/NoRoute() call below — gin snapshots a
	// group's middleware chain at Group()-call time (see the comment on
	// RequestBodySizeLimit in internal/adapter/middleware/body_size_limit.go
	// for the same footgun), so a later engine-level Use() would never reach
	// routes registered by SetApiRouter/SetApiV2Router/etc.
	router.Use(middleware.SecurityHeaders())

	// Expose /metrics endpoint for Prometheus scraping (restricted to private/loopback IPs)
	router.GET("/metrics", metricsAuthMiddleware(), gin.WrapH(promhttp.Handler()))

	SetApiRouter(router)
	SetApiV2Router(router)  // Multi-tenant v2 API routes
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetVideoRouter(router)
	SetInternalApiRouter(router)
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, buildFS, indexPage)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		})
	}
}

// metricsAuthMiddleware restricts /metrics to requests that can be shown to
// have come directly from inside the cluster.
//
// A private/loopback c.Request.RemoteAddr is NOT sufficient for that on its
// own, and must not be treated as the boundary: this deployment has no k8s
// Ingress in front of it — the edge is host nginx, proxy_pass'ing to a local
// NodePort, and both public vhosts unconditionally set X-Real-IP /
// X-Forwarded-For / X-Forwarded-Proto. That means the pod sees a
// private/cluster-internal RemoteAddr for every request that reaches it,
// public or not; checking RemoteAddr alone lets every public scrape through.
//
// The judgment actually used here:
//  1. RemoteAddr itself is public → reject outright (covers any future
//     topology where the pod really is reachable directly from the internet).
//  2. RemoteAddr is private/loopback AND no forwarding header is present →
//     this is a genuine direct in-cluster scrape (pod IP or node
//     loopback:NodePort) with no proxy in the path. Admit it. This must stay
//     the default-allow path: it's what every existing Prometheus-equivalent
//     scraper does today, and nothing gates it behind a token.
//  3. RemoteAddr is private/loopback BUT a forwarding header is present →
//     the request was relayed by nginx and could be from anyone; RemoteAddr
//     carries no signal anymore. An attacker can add a forwarding header but
//     cannot remove the one nginx adds, so its mere presence is trustworthy
//     even though its value isn't. Require a valid METRICS_AUTH_TOKEN bearer
//     token; reject (fail closed) if the env var isn't even configured.
func metricsAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			// RemoteAddr might not have a port (unlikely for TCP, but be safe)
			host = c.Request.RemoteAddr
		}

		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		if c.GetHeader("X-Forwarded-For") == "" && c.GetHeader("X-Real-IP") == "" && c.GetHeader("Forwarded") == "" {
			// No forwarding header: direct in-cluster scrape. Keep this the
			// unconditional default-allow path so METRICS_AUTH_TOKEN being
			// unset never breaks existing in-cluster scraping.
			c.Next()
			return
		}

		// A forwarding header is present, so this request was relayed by a
		// proxy (RemoteAddr no longer identifies the real client). Only an
		// explicit, correct bearer token authorizes it from here.
		token := os.Getenv("METRICS_AUTH_TOKEN")
		if token == "" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		const prefix = "Bearer "
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(token)) != 1 {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		c.Next()
	}
}
