package middleware

import (
	"net"

	"github.com/gin-gonic/gin"
)

// IsDirectInClusterRequest reports whether c looks like a request that hit
// the pod directly (loopback or RFC1918-private RemoteAddr with none of the
// three forwarding headers a reverse proxy adds present), rather than one
// relayed through the host nginx. It mirrors the classification
// router/main.go's metricsAuthMiddleware already uses for the same
// distinction (read, not imported — router owns that file and importing it
// from middleware would invert the package dependency). In today's
// topology this condition is met by the kubelet probing the NodePort/pod
// IP, and also by anything else on the node or cluster network calling the
// pod without going through a proxy. The two host nginx vhosts set
// X-Forwarded-For and X-Real-IP on what they relay
// (deploy/r6-host-nginx/*.conf:37-38,42-43; CLAUDE.md's K8s Deployment
// Facts), so a request that carries one of those headers is classified as
// not-direct — an external caller can add a forwarding header of its own,
// but that only makes it look relayed (i.e. rate limited), not direct.
func IsDirectInClusterRequest(c *gin.Context) bool {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port (unlikely for TCP, but be safe).
		host = c.Request.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return false
	}
	return c.GetHeader("X-Forwarded-For") == "" &&
		c.GetHeader("X-Real-IP") == "" &&
		c.GetHeader("Forwarded") == ""
}
