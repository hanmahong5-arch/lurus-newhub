package handler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ZitaLogin redirects the browser to identity.lurus.cn/login. The platform
// completes the OIDC dance and bounces back to return_to with the
// lurus_session cookie set on the parent .lurus.cn domain.
//
// SDK-native replacement for OIDCLoginRedirect (ADR-0011 Layer C).
// While migration is in flight both handlers exist; once user table has
// lurus_account_id and the v2 frontend wires this URL, the legacy path
// can be deleted.
//
// Query params:
//
//	return_to (optional) — must be a *.lurus.cn URL or rejected as
//	                       invalid_return_to (open-redirect guard).
func ZitaLogin(c *gin.Context) {
	if common.ZitaClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "zita_disabled",
			"message": "zita SDK not configured (IDENTITY_PUBLIC_URL/IDENTITY_SESSION_SECRET)",
		})
		return
	}

	returnTo := c.Query("return_to")
	if returnTo != "" && !isLurusReturnURL(returnTo) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_return_to",
			"message": "return_to must be an https URL on a *.lurus.cn host",
		})
		return
	}

	c.Redirect(http.StatusFound, common.ZitaClient.LoginRedirectURL(returnTo))
}

// isLurusReturnURL gates open-redirect by accepting only https URLs whose
// host ends in ".lurus.cn" (and rejecting bare "lurus.cn" matches like
// "evil.lurus.cn.attacker.example" which the suffix check would otherwise
// allow if naively written).
func isLurusReturnURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	return host == "lurus.cn" || strings.HasSuffix(host, ".lurus.cn")
}
