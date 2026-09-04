package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"

	"github.com/gin-gonic/gin"
)

const faultSimPath = "/api/v2/faultsim/v1/chat/completions"

func faultSimRoutes(t *testing.T) gin.RoutesInfo {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)
	return engine.Routes()
}

func hasFaultSimRoute(routes gin.RoutesInfo) bool {
	for _, rt := range routes {
		if rt.Path == faultSimPath {
			return true
		}
	}
	return false
}

// TestFaultSimRouteAbsentByDefault is the whole reason the fault simulator is
// allowed to exist.
//
// It passes trivially today — the point is that it can never be allowed to
// fail. A deliberately-broken upstream is a fine thing to have in UAT and an
// unacceptable thing to have in production, and the only structural guarantee
// separating the two is that the route is not registered unless FAULTSIM_TOKEN
// is set. That is a property of the router, so it is asserted against the real
// route table rather than by reading the source.
func TestFaultSimRouteAbsentByDefault(t *testing.T) {
	// No FAULTSIM_TOKEN in the environment.
	t.Setenv("FAULTSIM_TOKEN", "")

	if handler.FaultSimEnabled() {
		t.Fatal("FaultSimEnabled() is true with an empty FAULTSIM_TOKEN")
	}

	routes := faultSimRoutes(t)
	if len(routes) == 0 {
		t.Fatal("SetApiV2Router registered nothing — this test would pass vacuously")
	}
	if hasFaultSimRoute(routes) {
		t.Errorf("%s is registered without FAULTSIM_TOKEN. A route that can be made to "+
			"return 500s, 402s and half-written streams must not exist in an environment "+
			"nobody opted in for.", faultSimPath)
	}

	// Nothing anywhere near the fault simulator may be reachable either.
	for _, rt := range routes {
		if strings.Contains(rt.Path, "faultsim") {
			t.Errorf("unexpected faultsim route registered by default: %s %s", rt.Method, rt.Path)
		}
	}
}

// The other half: with the env set, the route MUST appear. Without this, the
// test above could stay green because the route was deleted, renamed or
// silently never registered at all — the classic vacuous pass.
func TestFaultSimRoutePresentWhenEnabled(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "test-token")

	if !handler.FaultSimEnabled() {
		t.Fatal("t.Setenv(FAULTSIM_TOKEN) did not take effect")
	}
	if !hasFaultSimRoute(faultSimRoutes(t)) {
		t.Fatalf("%s is not registered even with FAULTSIM_TOKEN set — "+
			"TestFaultSimRouteAbsentByDefault would then be measuring nothing", faultSimPath)
	}
}

// Enabling the simulator must not open an anonymous endpoint: the token is
// checked per request, so setting the env on the wrong instance is still not
// enough to drive it.
func TestFaultSimRequiresTokenPerRequest(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	body := `{"model":"http_500"}`

	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, faultSimPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 — the simulator must not be anonymous", w.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, faultSimPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Faultsim-Token", "nope")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("correct token as bearer", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, faultSimPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		// A seeded channel sends its key this way, so this shape has to work.
		req.Header.Set("Authorization", "Bearer test-token")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want the requested 500 fault", w.Code)
		}
	})
}

// Each mode must actually produce its named failure. A fault injector whose
// modes silently no-op would make every drill that used it a false green.
func TestFaultSimModesProduceTheirFault(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	call := func(t *testing.T, model, query string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		path := faultSimPath
		if query != "" {
			path += "?" + query
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"`+model+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Faultsim-Token", "test-token")
		engine.ServeHTTP(w, req)
		return w
	}

	t.Run("http_500", func(t *testing.T) {
		if w := call(t, handler.FaultModeHTTP500, ""); w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})

	t.Run("rate_limit_429 carries Retry-After", func(t *testing.T) {
		w := call(t, handler.FaultModeRateLimit429, "")
		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want 429", w.Code)
		}
		if w.Header().Get("Retry-After") == "" {
			t.Error("no Retry-After header — the rate-limit path would not be exercised")
		}
	})

	t.Run("upstream_insufficient_balance", func(t *testing.T) {
		w := call(t, handler.FaultModeInsufficientBalance, "")
		if w.Code != http.StatusPaymentRequired {
			t.Errorf("status = %d, want 402 — this is the shape that must classify as "+
				"upstream_insufficient_balance rather than upstream_4xx", w.Code)
		}
	})

	t.Run("slow_headers waits then answers", func(t *testing.T) {
		// A short delay keeps the test fast; the behaviour under test is that
		// nothing is written before it elapses.
		w := call(t, handler.FaultModeSlowHeaders, "delay_ms=10")
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 after the delay", w.Code)
		}
	})

	// The mode that justifies the whole handler: well-formed frames, then a
	// body that just stops. No [DONE], no finish_reason, no usage.
	t.Run("mid_stream_abort emits frames then stops without a terminator", func(t *testing.T) {
		w := call(t, handler.FaultModeMidStreamAbort, "frames=3")
		body := w.Body.String()
		if strings.Count(body, "data: ") < 3 {
			t.Errorf("expected at least 3 SSE frames, got:\n%s", body)
		}
		if strings.Contains(body, "[DONE]") {
			t.Errorf("emitted a terminator — then the stream did not abort, and the "+
				"incomplete-stream path is not exercised:\n%s", body)
		}
		if strings.Contains(body, "finish_reason") {
			t.Errorf("emitted a finish_reason — the abort must look unfinished:\n%s", body)
		}
	})

	t.Run("unknown mode is rejected, not silently ignored", func(t *testing.T) {
		if w := call(t, "not_a_mode", ""); w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 — a typo'd mode that answers 200 would make a "+
				"drill report success while injecting nothing", w.Code)
		}
	})

	// Every exported mode must be reachable: the list is what an operator reads.
	t.Run("every declared mode is implemented", func(t *testing.T) {
		for _, mode := range handler.FaultSimModes {
			w := call(t, mode, "delay_ms=1")
			if w.Code == http.StatusBadRequest {
				t.Errorf("mode %q is declared in FaultSimModes but rejected as unknown", mode)
			}
		}
	})
}
