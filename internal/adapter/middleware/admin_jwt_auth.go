package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AdminJWTAuth validates a OIDC JWT and requires the "admin" role.
// Used by v2 API routes that receive JWT tokens from external clients.
// For v1 web UI routes, use AdminAuth() (session-based) instead.
func AdminJWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// If no Authorization header, fall back to session-based admin auth
		// to support the web frontend which uses session cookies after OAuth login.
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			authHelper(c, common.RoleAdminUser)
			return
		}

		if !oidcEnabled || jwksManager == nil {
			authHelper(c, common.RoleAdminUser)
			return
		}

		claims, err := validateJWT(c)
		if err != nil {
			common.SysError(fmt.Sprintf("AdminJWTAuth: token validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "unauthorized",
			})
			c.Abort()
			return
		}

		roles := extractRoles(claims.Roles)
		if !hasRole(roles, "admin") && !hasRole(roles, "root") {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Admin role required",
			})
			c.Abort()
			return
		}

		c.Set("admin_sub", claims.Subject)
		c.Set("admin_email", claims.Email)
		c.Set("admin_roles", roles)
		if im, _ := common.GetAccountByZitadelSubGRPC(c.Request.Context(), claims.Subject); im != nil {
			c.Set("identity_account_id", im.ID)
		}

		c.Next()
	}
}

// RootJWTAuth is like AdminJWTAuth but requires the "root" role specifically.
// Falls back to session-based auth when no JWT Bearer token is present.
//
// The session fallback answers HTTP 403 (not v1's 200 {"success":false})
// when the caller is authenticated but not root — see rootSessionAuth. The
// Bearer-JWT branch below is unchanged.
func RootJWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			rootSessionAuth(c)
			return
		}

		if !oidcEnabled || jwksManager == nil {
			rootSessionAuth(c)
			return
		}

		claims, err := validateJWT(c)
		if err != nil {
			common.SysError(fmt.Sprintf("RootJWTAuth: token validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "unauthorized",
			})
			c.Abort()
			return
		}

		roles := extractRoles(claims.Roles)
		if !hasRole(roles, "root") {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Root role required",
			})
			c.Abort()
			return
		}

		c.Set("admin_sub", claims.Subject)
		c.Set("admin_email", claims.Email)
		c.Set("admin_roles", roles)

		c.Next()
	}
}

// rootSessionAuth is RootJWTAuth's session fallback: the same authorisation
// decision authHelper(c, RoleRootUser) makes — resolveSessionIdentity is
// called with the SAME minRole, so the set of admitted callers is unchanged —
// re-encoded for the v2 admin surface (cycle-12 §2).
//
// Why the re-encode: authHelper answers a caller who is logged in but lacks
// the role with HTTP 200 {"success":false}. That is v1's convention and
// switch consumes it, so authHelper keeps it (see
// TestRootDenialShape_V1AuthHelperShapeUnchanged). On /api/v2/admin/* the
// only consumer is the console, and a 2xx made every one of its pages run
// its success path on a refusal: the gateway page rendered "no open
// breakers", the settings page rendered every toggle as off. 403 with an
// error_code is what a fetch().catch() can actually branch on.
//
// Translating the response instead of re-deriving the role is deliberate:
// re-reading the session here would be a second copy of authHelper's
// resolution order (session / SDK-bridge cookie / platform session token,
// then the user-cache re-validation of status and role) that could drift
// from the original. The buffer only ever holds resolveSessionIdentity's own
// refusal envelope — on success it writes nothing and this never runs — and
// headers are NOT buffered (sessionDenialCapture embeds the real writer), so
// the Set-Cookie that authHelper's stale-cookie branches emit still reaches
// the browser.
func rootSessionAuth(c *gin.Context) {
	original := c.Writer
	capture := &sessionDenialCapture{ResponseWriter: original}
	c.Writer = capture
	admitted := resolveSessionIdentity(c, common.RoleRootUser)
	c.Writer = original
	if admitted {
		c.Next()
		return
	}
	capture.rewriteAsV2Denial(c)
}

// sessionDenialCapture buffers the body and status a middleware writes while
// it is installed, forwarding everything else (headers, flush, hijack) to the
// real writer it embeds. Mirrors relay.BufferedResponseWriter's embedding
// approach; it is installed for the duration of one resolveSessionIdentity
// call only, never around a handler, so it never buffers a streamed body.
type sessionDenialCapture struct {
	gin.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *sessionDenialCapture) WriteHeader(code int) {
	if code > 0 {
		w.status = code
	}
}

// WriteHeaderNow is gin's "flush the recorded status now" hook; suppressed so
// the buffered status cannot escape to the client ahead of the rewrite.
func (w *sessionDenialCapture) WriteHeaderNow() {}

func (w *sessionDenialCapture) Write(b []byte) (int, error) { return w.body.Write(b) }

func (w *sessionDenialCapture) WriteString(s string) (int, error) { return w.body.WriteString(s) }

func (w *sessionDenialCapture) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *sessionDenialCapture) Size() int { return w.body.Len() }

func (w *sessionDenialCapture) Written() bool { return w.status != 0 || w.body.Len() > 0 }

// rewriteAsV2Denial re-emits the buffered refusal on the real writer:
//
//	captured 200 -> 403, error_code PERMISSION_DENIED (added only if the
//	               envelope did not already carry one)
//	captured 401 -> 401, error_code UNAUTHENTICATED (added only if absent —
//	               SESSION_REVOKED, which authHelper sets on the same status,
//	               survives)
//	anything else -> replayed as-is
//
// The original message is kept: the 200-shaped branch covers "role too low",
// "user banned" and "invalid user info", and only authHelper knows which, so
// replacing all three with one sentence would tell a banned root the wrong
// thing. A body that is not a JSON object is replayed byte for byte.
func (w *sessionDenialCapture) rewriteAsV2Denial(c *gin.Context) {
	var env map[string]interface{}
	if w.body.Len() == 0 || json.Unmarshal(w.body.Bytes(), &env) != nil {
		c.Writer.WriteHeader(w.Status())
		if w.body.Len() > 0 {
			_, _ = c.Writer.Write(w.body.Bytes())
		}
		return
	}

	status := w.Status()
	switch status {
	case http.StatusOK:
		status = http.StatusForbidden
		if _, ok := env["error_code"]; !ok {
			env["error_code"] = "PERMISSION_DENIED"
		}
	case http.StatusUnauthorized:
		if _, ok := env["error_code"]; !ok {
			env["error_code"] = "UNAUTHENTICATED"
		}
	}
	c.JSON(status, env)
}

// validateJWT extracts and validates a Bearer JWT from the Authorization header.
func validateJWT(c *gin.Context) (*OIDCClaims, error) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return nil, errors.New("Authorization header required")
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	tokenString = strings.TrimPrefix(tokenString, "bearer ")
	if tokenString == authHeader {
		return nil, errors.New("Bearer token required")
	}

	token, err := jwt.ParseWithClaims(tokenString, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, errors.New("missing kid in token header")
		}
		return jwksManager.getKeyWithRefresh(kid)
	})

	if err != nil || !token.Valid {
		common.SysLog(fmt.Sprintf("admin JWT validation failed: %v", err))
		return nil, errors.New("invalid or expired token")
	}

	claims, ok := token.Claims.(*OIDCClaims)
	if !ok {
		return nil, errors.New("invalid or expired token")
	}

	// Use the same multi-issuer set as the main OIDCAuth middleware so that
	// a domain rebrand does not selectively break the admin path.
	adminIssuerOK := false
	for _, accepted := range oidcIssuers {
		if claims.Issuer == accepted {
			adminIssuerOK = true
			break
		}
	}
	if !adminIssuerOK {
		common.SysLog(fmt.Sprintf("admin JWT issuer mismatch: got %s, accepted %v", claims.Issuer, oidcIssuers))
		return nil, errors.New("invalid or expired token")
	}

	// Reject tokens minted for a different client/resource when audience
	// enforcement is opted in (OIDC_ALLOWED_AUDIENCES); see checkAudience in
	// oidc_auth.go. No-op when that var is unset.
	if !checkAudience(claims.Audience) {
		common.SysLog(fmt.Sprintf("admin JWT audience mismatch: got %v", []string(claims.Audience)))
		return nil, errors.New("invalid or expired token")
	}

	return claims, nil
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
