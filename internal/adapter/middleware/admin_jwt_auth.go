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
// The session fallback never answers a refusal with a 2xx (v1's 200
// {"success":false}): it answers 403 when the caller is authenticated and
// not allowed, and 401 when the credential itself is missing or invalid —
// see rootSessionAuth and rootSessionDenialsByMessage. The Bearer-JWT branch
// below is unchanged.
//
// Both fallback call sites go through it, not just the header-less one: with
// OIDC off (UAT's standing configuration) or its JWKS uninitialised, a
// request that DOES carry an Authorization header lands on the same route
// group and would otherwise have kept the 200-shaped refusal.
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
// refusal envelope — on success it writes nothing and this never runs.
//
// The Set-Cookie that auth.go's stale-cookie branches emit still reaches the
// browser, for two independent reasons: gin-contrib/sessions writes it
// through the writer it captured when the session middleware ran (which is
// the real one, not this capture), and sessionDenialCapture embeds the real
// writer so Header() is forwarded anyway. Only the first is load-bearing —
// verified by mutation: making the capture return a fresh http.Header from
// Header() does NOT drop the cookie, whereas removing the clear in auth.go
// does. TestRootJWTAuth_StaleCookieClearingSetCookieSurvivesTheCapture pins
// the end-to-end property (the 401 carries an expiring session cookie),
// which is what the browser depends on.
//
// WHICH refusal it was decides the new status: see
// rootSessionDenialsByMessage. A blanket "any 200 becomes 403" would tell a
// caller whose access token is simply invalid that it lacks a role, so it
// would never re-authenticate.
func rootSessionAuth(c *gin.Context) {
	admitted, capture := captureSessionDenial(c)
	if admitted {
		c.Next()
		return
	}
	capture.rewriteAsV2Denial(c)
}

// resolveRootSession is the seam rootSessionAuth resolves through. It is a
// var only so captureSessionDenial's panic safety can be tested with a
// resolver that panics on demand (TestRootSessionAuth_PanicRestoresWriter);
// production never reassigns it.
var resolveRootSession = func(c *gin.Context) bool {
	return resolveSessionIdentity(c, common.RoleRootUser)
}

// captureSessionDenial runs the session resolution with the capture
// installed and hands back both the decision and the buffer.
//
// The restore is deferred, not a plain assignment after the call: every
// lookup resolveSessionIdentity makes (repo.GetUserCache,
// repo.GetUserByIDPSubjectAnyTenant, repo.IsUserSessionRevoked,
// repo.TenantGate, common.GetAccountByZitadelSub_ByAccountID) can panic on a
// nil map/pointer, and a panic that unwound past a plain restore would leave
// the capture installed — gin.Recovery would then write its 500 into the
// discarded buffer and the client would receive a bare 200 with no body,
// which is the very 2xx-on-failure shape this lane exists to remove.
// c.Writer is restored before anything downstream runs, so the handler chain
// and the rewrite both write to the real writer.
func captureSessionDenial(c *gin.Context) (bool, *sessionDenialCapture) {
	original := c.Writer
	capture := &sessionDenialCapture{ResponseWriter: original}
	c.Writer = capture
	defer func() { c.Writer = original }()
	return resolveRootSession(c), capture
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

// rootSessionDenial is the v2 shape one of resolveSessionIdentity's HTTP 200
// refusals is re-encoded into.
type rootSessionDenial struct {
	status    int
	errorCode string
}

// rootSessionDenialsByMessage maps each HTTP 200 refusal resolveSessionIdentity
// can write to the status and error_code the v2 admin surface answers with.
// The key is the message auth.go writes, verbatim — auth.go is v1's contract
// and is not edited by this lane, so the message is the only thing that
// distinguishes the branches. The table is kept complete by
// TestRootSessionDenials_CoverEveryTwoHundredRefusal, which parses auth.go
// and fails when a 200-shaped refusal exists that is not listed here.
//
// The split is authentication vs authorisation, because a consumer does
// opposite things with them: 401 means "your credential is missing or no
// longer valid, get a new one"; 403 means "your credential is fine and it is
// not allowed to do this, stop retrying".
//
//	"...access token 无效"  -> 401 UNAUTHENTICATED. An invalid/expired
//	                          credential. Reachable on the branch RootJWTAuth
//	                          takes when an Authorization header is present
//	                          but OIDC is off or its JWKS never initialised
//	                          (admin_jwt_auth.go's `!oidcEnabled ||
//	                          jwksManager == nil`) — that is UAT's standing
//	                          configuration (OIDC_ENABLED=false) and prod's
//	                          whenever InitOIDCAuth failed.
//	"...用户信息无效"        -> 401 UNAUTHENTICATED. The identity resolved but
//	                          is not usable (blank username / role outside
//	                          the valid set): the session or token is corrupt,
//	                          not under-privileged.
//	"用户已被封禁"           -> 403 USER_DISABLED. Authenticated, recognised,
//	                          and refused for who they are; USER_DISABLED is
//	                          the spelling the v2 provisioning endpoints
//	                          already use for this state.
//	"...权限不足"            -> 403 PERMISSION_DENIED. The role shortfall the
//	                          lane exists for.
var rootSessionDenialsByMessage = map[string]rootSessionDenial{
	"无权进行此操作，access token 无效": {http.StatusUnauthorized, "UNAUTHENTICATED"},
	"无权进行此操作，用户信息无效":          {http.StatusUnauthorized, "UNAUTHENTICATED"},
	"用户已被封禁":                  {http.StatusForbidden, "USER_DISABLED"},
	"无权进行此操作，权限不足":            {http.StatusForbidden, "PERMISSION_DENIED"},
}

// rootSessionDenialFallback is what an unrecognised HTTP 200 refusal becomes.
// It is deliberately a refusal and never a 2xx: a new branch in auth.go must
// fail loudly at the completeness gate, not quietly pass a request through.
// 403 rather than 401 so an unknown refusal cannot be mistaken by a browser
// client for "log in again" and turned into a redirect loop.
var rootSessionDenialFallback = rootSessionDenial{http.StatusForbidden, "PERMISSION_DENIED"}

// classifyRootSessionDenial returns the v2 shape for a captured 200 refusal.
func classifyRootSessionDenial(message string) rootSessionDenial {
	if d, ok := rootSessionDenialsByMessage[message]; ok {
		return d
	}
	return rootSessionDenialFallback
}

// rewriteAsV2Denial re-emits the buffered refusal on the real writer:
//
//	captured 200 -> the status and error_code classifyRootSessionDenial gives
//	               for that refusal's own message (error_code added only if
//	               the envelope did not already carry one)
//	captured 401 -> 401, error_code UNAUTHENTICATED (added only if absent —
//	               SESSION_REVOKED, which authHelper sets on the same status,
//	               survives; pinned by
//	               TestRootJWTAuth_SessionRevokedCodeSurvivesTheRewrite)
//	anything else -> replayed as-is (e.g. the 403 TENANT_DISABLED branch,
//	               which already has the v2 shape)
//
// The original message is always kept: a banned root and a root whose role
// was demoted get different sentences, as they did before this lane. A body
// that is not a JSON object is replayed byte for byte.
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
		message, _ := env["message"].(string)
		denial := classifyRootSessionDenial(message)
		status = denial.status
		if _, ok := env["error_code"]; !ok {
			env["error_code"] = denial.errorCode
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
