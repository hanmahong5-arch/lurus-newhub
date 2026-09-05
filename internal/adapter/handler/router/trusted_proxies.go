package router

// ConfigureTrustedProxies wires gin's trust boundary for X-Forwarded-For.
// Without this, gin trusts every hop in the chain and ClientIP() returns the
// left-most (fully caller-controlled) entry of X-Forwarded-For. Production
// has exactly one real hop between the client and this process — the host
// nginx reverse proxy in front of the NodePort, which appends via
// $proxy_add_x_forwarded_for — so any address outside cidrs must never be
// trusted to have set that header honestly. ClientIP() in turn feeds the
// IP-keyed rate limiters, the token IP allow-list, audit logging and log
// persistence, all of which a caller could otherwise bypass by varying the
// header per request.

import (
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ConfigureTrustedProxies applies cidrs as engine's trusted proxy list. It
// never panics: a malformed CIDR is returned as an error so the caller can
// fail boot loudly instead of silently running with an unbounded trust
// boundary.
func ConfigureTrustedProxies(engine *gin.Engine, cidrs []string) error {
	if err := engine.SetTrustedProxies(cidrs); err != nil {
		return err
	}
	common.SysLog("trusted proxies configured: " + strings.Join(cidrs, ","))
	return nil
}
