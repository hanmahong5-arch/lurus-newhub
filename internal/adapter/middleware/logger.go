package middleware

import (
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// SetUpLogger registers the access-log middleware. It branches ONCE, at
// registration time, on the format InitSlog last applied
// (common.IsJSONLogFormat, set from LOG_FORMAT / GIN_MODE — see
// cmd/server/main.go's InitResources, which calls common.InitSlog before
// run() reaches this call; TestMainGo_AccessLoggerWiredAfterInitSlog
// in r6b_access_log_wiring_test.go pins that source-order dependency against
// the real cmd/server/main.go rather than a hand-copy of it) instead of
// re-reading the env var itself. That closes the drift the env-var-reread
// would have had, but it is still a snapshot: if InitSlog is invoked again
// with a different format AFTER this call registered its branch, the
// already-registered middleware keeps answering the format that was live when
// it was registered, not whatever IsJSONLogFormat() reports now. Non-test
// InitSlog call sites, grepped 2026-08-27: cmd/server/main.go's InitResources
// (the boot-time one), and slog.go's ensureSlogInit lazy fallback, which calls
// InitSlog(nil) — text format — only while slogLogger is still nil, so after
// boot it cannot fire. Within this test package, trivial_cover_test.go's
// TestSetUpLogger_DoesNotPanic calls SetUpLogger without a fresh preceding
// InitSlog and therefore depends on whatever format an earlier test left in
// global slog state.
//
// Text mode keeps the legacy "[GIN] ..." line byte-for-byte. JSON mode
// (LOG_FORMAT=json — which is what the live deployment sets, see
// deploy/k8s/r6-stage/deployment.yaml:195-196, so JSON is the branch
// production actually takes and the "[GIN] " lines operators read via
// `kubectl logs` today are replaced by http_request records) routes
// through the same structured logger as every other log line, so the access
// log picks up trace_id/request_id injection (contextHandler.Handle in
// internal/pkg/common/slog.go) and carries identity dimensions the text
// formatter never had room for. That routing also means the access log now
// inherits LOG_LEVEL: common.LogInfo is gated by the same slogLevel as every
// other structured log, so LOG_LEVEL=warn (or above) silently drops every
// access-log line — a coupling the old gin.LoggerWithFormatter/Fprintf path
// never had (production leaves LOG_LEVEL unset today, so this is dormant,
// not triggered — deploy/k8s/r6-stage/deployment.yaml has no LOG_LEVEL key).
func SetUpLogger(server *gin.Engine) {
	if common.IsJSONLogFormat() {
		server.Use(jsonAccessLogger())
		return
	}
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			if v, ok := param.Keys[common.RequestIdKey].(string); ok {
				requestID = v
			}
		}
		return fmt.Sprintf("[GIN] %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			param.Path,
		)
	}))
}

// jsonAccessLogger emits one structured "http_request" record per request via
// common.LogInfo instead of hand-writing its own JSON encoder, so it shares
// the JSON handler with every other log line.
//
// Identity fields are read AFTER c.Next(): the auth middlewares populate
// "id"/"token_id"/"token_name" via c.Set (auth.go, flex_auth.go,
// oidc_auth.go) and InjectTenantContext populates "tenant_id"
// (internal/adapter/repo/tenant_context.go) during the handler chain, which
// runs after this middleware in registration order (SetUpLogger is registered
// before the routers — cmd/server/main.go). A request that is rejected before
// any of those writes — an anonymous or failed-auth request — leaves the keys
// absent, and c.GetInt/c.GetString return the zero value for them, so the
// record schema stays stable instead of omitting fields conditionally. Read a
// zero auth_user_id as "no identity was established by the time this record
// was emitted", not as "user 0".
//
// "auth_user_id" (int, this call) and "user_id" (string, injected below by
// common's contextHandler from the request context — see slog.go's
// contextHandler.Handle) are deliberately two DIFFERENT keys, not the same
// key written twice. They come from two different writes, and the set of code
// paths performing each is not the same. Enumerated 2026-08-27 by grepping
// non-test sources for `c.Set("id"` (7 sites) and `common.WithUserID` (3
// sites):
//
//	c.Set("id") + common.WithUserID — both keys present:
//	  authHelper            auth.go:216 + :222   (UserAuth/AdminAuth/RootAuth,
//	                                              i.e. the console session path)
//	  TokenAuth             auth.go:564 + :525   (:564 is inside
//	                                              SetupContextForToken, which
//	                                              TokenAuth calls; this is the
//	                                              relay/billing path)
//	  flexAuthViaToken      flex_auth.go:81 + :86
//
//	c.Set("id") only — "auth_user_id" present, "user_id" absent:
//	  auth.go:280 (TryUserAuth), auth.go:368 (TokenAuth's admin
//	  impersonation branch), auth.go:658 (PlaygroundAuth),
//	  flex_auth.go:100 (flexAuthViaJWT), oidc_auth.go:589 and :632
//
// Collapsing both writes onto one "user_id" key used to emit it twice per
// request (int then string) on the three paths that do both — strict JSON
// consumers (duplicate-key rejection) could drop those records outright, and
// lenient last-wins consumers would silently coerce every numeric user_id
// query to the string variant. Two distinctly-named keys removes the collision
// instead of picking a lossy winner.
func jsonAccessLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		common.LogInfo(c.Request.Context(), "http_request",
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"client_ip", c.ClientIP(),
			"auth_user_id", c.GetInt("id"),
			"tenant_id", c.GetString("tenant_id"),
			"token_id", c.GetInt("token_id"),
		)
	}
}
