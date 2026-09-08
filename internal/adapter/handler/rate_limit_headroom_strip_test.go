package handler

// rate_limit_headroom_strip_test.go — L1-RL-HEADROOM item 4 lock: an
// upstream-originated error response must not carry the gateway's own
// admit-path X-RateLimit-* headroom headers.
//
// This drives the REAL Relay() handler (not an extracted helper) on a bare
// gin.Context, in the style of relay_sensitive_rejection_test.go /
// relay_preauth_release_test.go elsewhere in this package — no DB, no
// httptest upstream, no goroutines. That is a deliberate choice, not a
// shortcut: an earlier version of this test drove a full DB-backed
// TokenAuth->Distribute->BusinessRateLimit->Relay router (mirroring
// relay_success_fixture_test.go's harness) behind a real httptest 429
// upstream. It passed in isolation but, once seeded in the SAME package as
// that fixture, corrupted sibling tests (repo/pricing.go's package-level
// GetPricing 1-minute cache — populated from this test's hermetic sqlite
// handle — outlived this test's t.Cleanup() and was later read by another
// test's own, freshly-migrated, differently-shaped DB, tripping "no such
// table: abilities"). That cross-test global-state hazard belongs to
// repo/pricing.go's caching design (out of this lane's file list), not to
// this fix, so the safer route is a request whose error is 5xx-classified
// without needing a real channel round trip at all — reachable from a
// gateway-side pre-channel failure (GenRelayInfo's price-lookup step
// defaults *types.NewAPIError to 500 when a model has no configured ratio,
// exactly types.NewError's documented default), exercising the SAME
// isUpstreamOriginatedError predicate a genuine upstream 5xx/429 would.
//
// TestRelay_NonUpstreamError_KeepsGatewayHeadroomHeaders below extends the
// same seam with a request-too-large (413) failure, which is deliberately
// NOT upstream-classified, to prove the strip is conditional, not blanket.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// presetGatewayHeadroomHeaders writes the same header set
// middleware.setRateLimitHeadroomHeaders would have written on an earlier,
// successful BusinessRateLimit admit check in the same request — the
// deferred writer in Relay() never distinguishes who wrote them, so setting
// them directly here exercises exactly the same code it would run against a
// real middleware-written snapshot.
func presetGatewayHeadroomHeaders(c *gin.Context) {
	c.Writer.Header().Set("X-RateLimit-Limit", "5")
	c.Writer.Header().Set("X-RateLimit-Remaining", "3")
	c.Writer.Header().Set("X-RateLimit-Reset", "9999999999")
	c.Writer.Header().Set("X-RateLimit-Scope", "token")
	c.Writer.Header().Set("X-RateLimit-Type", "rpm")
}

// TestRelay_UpstreamOriginatedError_StripsGatewayHeadroomHeaders is the
// item-4 lock: GenRelayInfo's price lookup fails closed with a
// types.NewError default of 500 (types.IsUpstreamFailure classifies any
// unclassified 5xx as upstream — error.go's documented "fail-safe" default,
// the SAME predicate a real provider 5xx or 429 would hit) when the
// requested model has no configured ratio and no channel was ever attempted
// — the gateway's own headroom snapshot from an earlier admit check must not
// survive onto that response.
func TestRelay_UpstreamOriginatedError_StripsGatewayHeadroomHeaders(t *testing.T) {
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMB })

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"model":"l1-headroom-strip-unpriced-model","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	presetGatewayHeadroomHeaders(c)

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (types.NewError's documented default for an unclassified error — see types.IsUpstreamFailure's fail-safe comment); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upstream provider") {
		t.Fatalf("body does not carry the upstream-attribution message this test relies on being upstream-classified: %s", w.Body.String())
	}
	for _, h := range []string{"X-RateLimit-Scope", "X-RateLimit-Type", "X-RateLimit-Remaining", "X-RateLimit-Limit", "X-RateLimit-Reset"} {
		if v := w.Header().Get(h); v != "" {
			t.Errorf("%s = %q, want absent — this is an earlier, unrelated admit-path snapshot, not a fact about this upstream-classified failure", h, v)
		}
	}
}

// TestRelay_NonUpstreamError_KeepsGatewayHeadroomHeaders is the control:
// item 4 only strips headers from upstream-originated failures. A request
// body over MaxRequestBodyMB is a client/gateway-side rejection (413,
// SkipRetry, not upstream — types.IsUpstreamFailure is false for 4xx other
// than the timeout codes, and 413 != 429) — its response must still carry
// whatever admit-path headroom an earlier check wrote.
func TestRelay_NonUpstreamError_KeepsGatewayHeadroomHeaders(t *testing.T) {
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 0 // 0 → GetAndValidateRequest rejects every body as too large
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMB })

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	presetGatewayHeadroomHeaders(c)

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", w.Code, w.Body.String())
	}
	if scope := w.Header().Get("X-RateLimit-Scope"); scope != "token" {
		t.Errorf("X-RateLimit-Scope = %q, want token — a non-upstream failure must not touch the earlier admit-path snapshot", scope)
	}
	if lim := w.Header().Get("X-RateLimit-Limit"); lim != "5" {
		t.Errorf("X-RateLimit-Limit = %q, want 5", lim)
	}
	if rem := w.Header().Get("X-RateLimit-Remaining"); rem != "3" {
		t.Errorf("X-RateLimit-Remaining = %q, want 3", rem)
	}
}

// TestRelay_AllKeysCooling_StripsGatewayHeadroomHeaders is the residuals-round-2
// item-1 lock: relay.go's all-keys-cooling special case (types.ErrorCodeChannel
// AllKeysCooling -> 503 + Retry-After) must fall through to the same
// isUpstreamOriginatedError strip the generic upstream-error test above proves,
// not return before it. It drives the real Relay() end to end (not a slice of
// the closure) via getChannelFn — a call seam for channel selection — so no
// DB-backed multi-key channel/cooldown fixture is needed to reach the branch.
func TestRelay_AllKeysCooling_StripsGatewayHeadroomHeaders(t *testing.T) {
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMB })

	// A model with an explicit zero ratio makes ModelPriceHelper report
	// FreeModel — PreConsumeQuota is skipped, so no seeded user/DB is needed
	// to reach the channel-selection retry loop.
	const model = "l1-allkeyscooling-strip-test-model"
	ratioSnapshot := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(ratioSnapshot) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + model + `":0}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	qs := operation_setting.GetQuotaSetting()
	prevFreeConsume := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prevFreeConsume })

	prevGetChannel := getChannelFn
	t.Cleanup(func() { getChannelFn = prevGetChannel })
	retryAfterUnix := time.Now().Add(45 * time.Second).Unix()
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, retryParam *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		apiErr := types.NewError(nil, types.ErrorCodeChannelAllKeysCooling)
		apiErr.RetryAfterUnix = retryAfterUnix
		return nil, apiErr
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	presetGatewayHeadroomHeaders(c)

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Errorf("Retry-After absent, want the all-keys-cooling deadline")
	}
	for _, h := range []string{"X-RateLimit-Scope", "X-RateLimit-Type", "X-RateLimit-Remaining", "X-RateLimit-Limit", "X-RateLimit-Reset"} {
		if v := w.Header().Get(h); v != "" {
			t.Errorf("%s = %q, want absent — all-keys-cooling is upstream-classified (503) and must strip the earlier admit-path snapshot same as any other upstream failure", h, v)
		}
	}
}

// TestIsUpstreamOriginatedError_Predicate is the residuals-round-2 item-2
// oracle for isUpstreamOriginatedError (relay.go): a plain-4xx error
// (413/400) must NOT be upstream-classified — that predicate feeds the
// headroom-strip decision above, and a 4xx caller mistake stripping the
// gateway's own admit-path headers would be as wrong as a 5xx NOT stripping
// them. The 429 arm is deliberately on top of (not instead of)
// types.IsUpstreamFailure, which by itself excludes plain 4xx — this table
// proves the OR actually flips 429 specifically, with 413/400 as controls
// that show it is not a blanket 4xx pass.
func TestIsUpstreamOriginatedError_Predicate(t *testing.T) {
	cases := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{"nil", nil, false},
		{"429_too_many_requests", &types.NewAPIError{StatusCode: http.StatusTooManyRequests}, true},
		{"413_request_too_large", &types.NewAPIError{StatusCode: http.StatusRequestEntityTooLarge}, false},
		{"400_bad_request", &types.NewAPIError{StatusCode: http.StatusBadRequest}, false},
		{"503_service_unavailable_via_IsUpstreamFailure", &types.NewAPIError{StatusCode: http.StatusServiceUnavailable}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUpstreamOriginatedError(tc.err); got != tc.want {
				t.Errorf("isUpstreamOriginatedError(%+v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRelay_UpstreamRetryAfter_ForwardedWhenGatewayDidNotSetOne is the
// residuals-round-2 item-2 forwarding lock for the UpstreamHeader Retry-After
// forwarding block in Relay()'s error defer (relay.go): once an
// upstream-originated failure has stripped the gateway's own admit-path
// headroom headers, the provider's OWN Retry-After (stashed on
// NewAPIError.UpstreamHeader by RelayErrorHandler) must take their place —
// it is the one retry hint that is actually about this response. Uses a
// plain 429 (not all-keys-cooling, which sources its own Retry-After from
// RetryAfterUnix instead) so this exercises the UpstreamHeader forwarding
// branch specifically, and the 429 arm of isUpstreamOriginatedError from the
// test above end to end through the real Relay() handler.
func TestRelay_UpstreamRetryAfter_ForwardedWhenGatewayDidNotSetOne(t *testing.T) {
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMB })

	const model = "l1-upstream-retry-forward-test-model"
	ratioSnapshot := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(ratioSnapshot) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + model + `":0}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	qs := operation_setting.GetQuotaSetting()
	prevFreeConsume := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prevFreeConsume })

	prevGetChannel := getChannelFn
	t.Cleanup(func() { getChannelFn = prevGetChannel })
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, retryParam *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		apiErr := types.NewErrorWithStatusCode(errors.New("provider rate limited"),
			types.ErrorCodeGetChannelFailed, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
		apiErr.UpstreamHeader = http.Header{"Retry-After": []string{"17"}}
		return nil, apiErr
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	presetGatewayHeadroomHeaders(c)

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
	}
	if ra := w.Header().Get("Retry-After"); ra != "17" {
		t.Errorf("Retry-After = %q, want %q (forwarded from NewAPIError.UpstreamHeader)", ra, "17")
	}
	for _, h := range []string{"X-RateLimit-Scope", "X-RateLimit-Type", "X-RateLimit-Remaining", "X-RateLimit-Limit", "X-RateLimit-Reset"} {
		if v := w.Header().Get(h); v != "" {
			t.Errorf("%s = %q, want absent — a plain upstream 429 is upstream-classified and must strip the earlier admit-path snapshot", h, v)
		}
	}
}
