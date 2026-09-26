package middleware

// cors_relay_plane_test.go — cycle-14 L6 (PA): the console's browser origin
// allow-list was mounted on the machine relay plane, where it protected
// nothing (bearer credential, no cookie, so no cross-site request forgery
// surface; an attacker just omits Origin) and refused every honest browser
// client with a 403 and a ZERO-LENGTH body, before authentication.
//
// These tests drive the real middleware in this package. They do NOT drive
// the router files that mount it — relay-router.go:14 and dashboard.go:14
// belong to the wiring lane; TestRelayCORS_ConsoleGroupsAreBuiltBeforeTheRelayMount
// below is the only thing here that reads a router file, and it reads it as
// text.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// relayPlaneEngine builds an engine shaped like the relay router: the CORS
// posture on the engine, then a stand-in for TokenAuth that records whether
// the request ever got that far and answers 401 like the real one does.
func relayPlaneEngine(t *testing.T, posture gin.HandlerFunc, reachedAuth *bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(posture)
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		*reachedAuth = true
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "no key"}})
	})
	return r
}

// TestRelayCORS_CrossOriginRelayRequestReachesAuth is the PA oracle: a
// browser POST to the relay with an Origin header must be handled by the
// route (i.e. reach authentication), not refused by the CORS middleware.
func TestRelayCORS_CrossOriginRelayRequestReachesAuth(t *testing.T) {
	withCORSTestOrigin(t, "https://console.example")

	reached := false
	r := relayPlaneEngine(t, RelayCORS(), &reached)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://app.customer.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-whatever")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !reached {
		t.Fatalf("a cross-origin relay POST never reached the route (status=%d, body=%q) — the origin allow-list is refusing machine-API callers before authentication",
			w.Code, w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 from the auth stand-in", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want \"*\" on the machine plane", got)
	}
}

// TestRelayCORS_PermissiveOriginButNoCredentials pins the pairing that makes
// the permissive origin safe: a browser must not be able to attach ambient
// cookies to a cross-origin relay call.
func TestRelayCORS_PermissiveOriginButNoCredentials(t *testing.T) {
	withCORSTestOrigin(t, "https://console.example")

	reached := false
	r := relayPlaneEngine(t, RelayCORS(), &reached)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://app.customer.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want absent — the relay plane authenticates with a bearer key, never a cookie", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want \"*\"", got)
	}
}

// TestRelayCORS_PreflightAdmitsNativeWireHeaders: a browser client speaking
// the Claude or Gemini native wire must be able to send the credential and
// version headers those wires require, or the preflight strips them before
// the real request leaves the client.
func TestRelayCORS_PreflightAdmitsNativeWireHeaders(t *testing.T) {
	withCORSTestOrigin(t, "https://console.example")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RelayCORS())
	r.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/v1/messages", nil)
	req.Header.Set("Origin", "https://app.customer.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-api-key,anthropic-version,anthropic-beta,x-goog-api-key,mj-api-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 204/200; body=%q", w.Code, w.Body.String())
	}
	allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	for _, h := range []string{"x-api-key", "anthropic-version", "anthropic-beta", "x-goog-api-key", "mj-api-secret"} {
		if !strings.Contains(allowed, h) {
			t.Errorf("Access-Control-Allow-Headers = %q, missing %q", allowed, h)
		}
	}
}

// TestCORS_RefusalCarriesEnvelopeWithCodeAndRequestId is the second half of
// PA: whatever refusal remains on the console plane must be debuggable. The
// library answers a disallowed origin with an empty 403; a customer gets no
// code, no message and no request id.
func TestCORS_RefusalCarriesEnvelopeWithCodeAndRequestId(t *testing.T) {
	withCORSTestOrigin(t, "https://console.example")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestId())
	r.Use(CORS())
	r.POST("/api/user/self", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/api/user/self", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a disallowed console origin", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Fatal("refusal body is zero-length — no code, no message, no request id for the customer to report")
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("refusal body is not a JSON envelope: %v (body=%q)", err, w.Body.String())
	}
	if code, _ := envelope.Error.Code.(string); code != "access_denied" {
		t.Errorf("error.code = %v, want access_denied", envelope.Error.Code)
	}
	reqID := w.Header().Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("no X-Request-Id on the refusal — nothing to correlate a support ticket with")
	}
	if !strings.Contains(envelope.Error.Message, reqID) {
		t.Errorf("error.message = %q, want it to carry the request id %q", envelope.Error.Message, reqID)
	}
	// The refusal must still not hand the caller CORS permission.
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty on a refusal", got)
	}
}

// TestCORS_AllowedConsoleOriginStillGetsCredentialedHeaders guards the other
// direction: rebuilding the allow-list check inside this package must not
// change what a legitimate console origin receives.
func TestCORS_AllowedConsoleOriginStillGetsCredentialedHeaders(t *testing.T) {
	withCORSTestOrigin(t, "https://console.example")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS())
	r.GET("/api/status", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Origin", "https://console.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an allow-listed origin; body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the echoed origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true on the console plane", got)
	}
}

// TestCORS_SameOriginRequestIsUntouched: fetch() sets Origin on same-origin
// POSTs too. Those must pass through without a CORS decision at all.
func TestCORS_SameOriginRequestIsUntouched(t *testing.T) {
	withCORSTestOrigin(t, "https://console.example")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS())
	r.POST("/api/user/self", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "http://hub.internal/api/user/self", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://hub.internal")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("same-origin POST status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Mount-order assumption.
//
// The posture split relies on gin snapshotting a group's middleware chain at
// Group() call time: SetRelayRouter's engine-level Use reaches only routes
// registered AFTER it, so the console groups (/api, /api/v2, and the
// dashboard "/" group) keep their own strict CORS. If SetRouter ever built
// the relay router first, every console preflight would be answered by the
// permissive posture with "Access-Control-Allow-Origin: *" and no
// Allow-Credentials, and every credentialed cross-origin console call would
// break — with all the tests above still green.
//
// BLIND SPOT: this reads main.go as TEXT and only compares the position of
// four call sites. It cannot see a mount moved INSIDE one of those
// functions, a fifth router file, a group created lazily inside a handler,
// or any of this at runtime. It answers exactly one question: does SetRouter
// still call the console routers before the relay router.
// ---------------------------------------------------------------------------
// consoleRouterCalls are the console (cookie-authenticated) router setups
// that must be called before the relay router's engine-level Use.
var consoleRouterCalls = []string{"SetApiRouter(router)", "SetApiV2Router(router)", "SetDashboardRouter(router)"}

// mountOrderViolations returns one message per console router that SetRouter
// builds after the relay router, plus one per call site it can no longer
// find at all (a gate that cannot find its subject must say so, not pass).
func mountOrderViolations(src string) []string {
	var out []string
	relayAt := strings.Index(src, "SetRelayRouter(router)")
	if relayAt < 0 {
		return []string{"SetRelayRouter(router) not found — this gate cannot verify anything; fix the gate rather than deleting it"}
	}
	for _, console := range consoleRouterCalls {
		at := strings.Index(src, console)
		if at < 0 {
			out = append(out, console+" not found — gate cannot verify the ordering it exists for")
			continue
		}
		if at > relayAt {
			out = append(out, console+" is called AFTER SetRelayRouter(router): those routes would inherit the relay plane's permissive, credential-less CORS posture instead of the console allow-list")
		}
	}
	return out
}

func TestRelayCORS_ConsoleGroupsAreBuiltBeforeTheRelayMount(t *testing.T) {
	path := filepath.Join("..", "handler", "router", "main.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, v := range mountOrderViolations(string(raw)) {
		t.Errorf("%s: %s", path, v)
	}
}

// TestRelayCORS_MountOrderGateCatchesAReorderedSource is the gate's own
// mutation check: fed a SetRouter that builds the relay router first, the
// comparison above must report it. Without this, a gate that always returned
// "no violations" would look identical to a correct one.
func TestRelayCORS_MountOrderGateCatchesAReorderedSource(t *testing.T) {
	reordered := `func SetRouter(router *gin.Engine) {
	SetRelayRouter(router)
	SetApiRouter(router)
	SetApiV2Router(router)
	SetDashboardRouter(router)
}`
	got := mountOrderViolations(reordered)
	if len(got) != len(consoleRouterCalls) {
		t.Errorf("mountOrderViolations on a relay-first SetRouter returned %d violation(s), want %d: %v", len(got), len(consoleRouterCalls), got)
	}
	if v := mountOrderViolations("func SetRouter() {}"); len(v) != 1 {
		t.Errorf("a source with no SetRelayRouter call must yield exactly the cannot-verify message, got %v", v)
	}
}
