package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// wire_format_test.go is the route table for relayFormatForPath: it asserts
// the wire a middleware-stage rejection answers in, for a HAND-PICKED
// SELECTION of request shapes.
//
// It is NOT exhaustive and must not be read as such: relay-router.go alone
// registers well over sixty route shapes (httpRouter ~29, plus modelsRouter,
// billingRouter, responsesStateRouter, the two Midjourney groups, suno,
// music, and the task/video routers), and shapes such as /v1/realtime,
// /v1/completions, /v1/responses, /v1/images/*, /v1/audio/*, /v1/rerank,
// /v1/moderations, /v1/files*, /v1/fine-tunes* and /v1/responses/:id have no
// cell here. The cells below are chosen for the boundaries the rules turn on
// (the /v1/models method split, the /v1/engines shape, the /v1beta narrowing,
// the caller-chosen :mode segment), not for coverage of the router.
//
// BLIND SPOT: relayFormatForPath sees ONLY the request method and URL path.
// It cannot see headers, query or body, so a caller that picks its dialect by
// header on a shared route — modelsRouter's GET /v1/models dispatches to the
// Gemini or Anthropic model listing on x-goog-api-key / anthropic-version —
// still gets the OpenAI envelope from this function. It also cannot tell that
// a route exists at all: a path nobody registered gets a format here just the
// same (several cells below are exactly that), and a route group that never
// mounts StampRelayFormat() is invisible to this table
// (relay_wire_stamp_test.go in the router package is what proves the mount).
// Nothing here is derived FROM relay-router.go, so a route added there gets
// no cell until a human adds one.
func TestRelayFormatForPath_PerRoute(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   types.RelayFormat
	}{
		{"claude messages", http.MethodPost, "/v1/messages", types.RelayFormatClaude},
		{"claude count_tokens", http.MethodPost, "/v1/messages/count_tokens", types.RelayFormatClaude},
		{"openai chat", http.MethodPost, "/v1/chat/completions", types.RelayFormatOpenAI},
		{"openai embeddings", http.MethodPost, "/v1/embeddings", types.RelayFormatOpenAI},
		{"openai responses compact", http.MethodPost, "/v1/responses/compact", types.RelayFormatOpenAI},
		{"native gemini v1beta", http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", types.RelayFormatGemini},
		{"gemini v1beta model listing", http.MethodGet, "/v1beta/models", types.RelayFormatGemini},
		// The OpenAI-compatible subtree. The first cell is the shape
		// geminiCompatibleRouter actually registers (relay-router.go:53); the
		// second is a path nobody registers (measured: 404 through the real
		// SetRelayRouter) kept as a shape guard on the rule.
		{"gemini openai-compatible listing", http.MethodGet, "/v1beta/openai/models", types.RelayFormatOpenAI},
		{"gemini openai-compatible subtree", http.MethodPost, "/v1beta/openai/chat/completions", types.RelayFormatOpenAI},

		// The two routes relay-router.go hands to types.RelayFormatGemini from
		// under /v1 (httpRouter: POST /engines/:model/embeddings and POST
		// /models/*path). Before this table they fell to the OpenAI default,
		// so a middleware rejection and a relay-stage failure on the SAME
		// route answered in two different envelopes.
		{"gemini v1 engines embeddings", http.MethodPost, "/v1/engines/text-embedding-004/embeddings", types.RelayFormatGemini},
		{"gemini v1 models generateContent", http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", types.RelayFormatGemini},
		{"gemini v1 models streamGenerateContent", http.MethodPost, "/v1/models/gemini-1.5-pro:streamGenerateContent", types.RelayFormatGemini},

		// ...and the sibling OpenAI routes on the same prefixes, which must
		// NOT be swept up by the rule above: model listing/retrieval is GET,
		// and the not-implemented delete is DELETE.
		{"openai model listing", http.MethodGet, "/v1/models", types.RelayFormatOpenAI},
		{"openai model retrieve", http.MethodGet, "/v1/models/gpt-4o", types.RelayFormatOpenAI},
		{"openai model delete (not implemented)", http.MethodDelete, "/v1/models/gpt-4o", types.RelayFormatOpenAI},

		// Shape guards for the /v1/engines rule: only the exact
		// /v1/engines/<one segment>/embeddings shape is Gemini's.
		{"engines without the embeddings tail", http.MethodPost, "/v1/engines/text-embedding-004", types.RelayFormatOpenAI},
		{"engines with a deeper tail", http.MethodPost, "/v1/engines/m/embeddings/extra", types.RelayFormatOpenAI},

		// One cell per relay group that does NOT mount StampRelayFormat
		// (relay-router.go: modelsRouter, playgroundRouter, billingRouter,
		// relayMjRouter, relayMjModeRouter, relaySunoRouter,
		// relayMusicRouter; task-router.go: /v1/tasks; video-router.go: the
		// /v1 video group, /kling/v1, jimeng). Each must resolve to the
		// OpenAI default here, which is exactly what renderRejection falls
		// back to when nothing was stamped — so the missing mount is a no-op
		// today, and stays a no-op if someone mounts the stamp on those
		// groups later.
		//
		// What these cells can and cannot do: they pin the ANSWER for the
		// one path spelled in each cell. They do not enumerate the paths a
		// group serves, so a group whose prefix is a wildcard is only as
		// well covered as the values a human thought to spell out — see the
		// :mode pair below, where the benign value passed both before and
		// after the /v1beta narrowing and only the hostile one moved. See
		// also the BLIND SPOT note: this table cannot see whether a group
		// mounts the stamp at all.
		{"unstamped: playground", http.MethodPost, "/pg/chat/completions", types.RelayFormatOpenAI},
		{"unstamped: self-billing", http.MethodGet, "/v1/billing/balance", types.RelayFormatOpenAI},
		{"unstamped: midjourney", http.MethodPost, "/mj/submit/imagine", types.RelayFormatOpenAI},
		{"unstamped: midjourney mode", http.MethodPost, "/fast/mj/submit/imagine", types.RelayFormatOpenAI},
		// :mode is a FREE segment (relay-router.go:247 registers the group as
		// "/:mode/mj"), so the caller — not the router — picks it. Measured
		// through the real SetRelayRouter: POST /v1beta/mj/submit/imagine is
		// live today (gin backtracks past the /v1beta group to the :mode
		// wildcard) and answers 401 in the OpenAI envelope. Any :mode-agnostic
		// cell above hardcodes one benign value and cannot see that; this cell
		// picks the hostile one, and it is what pins the /v1beta rule to the
		// shapes relay-router.go actually registers under /v1beta.
		{"unstamped: midjourney mode shadowing /v1beta", http.MethodPost, "/v1beta/mj/submit/imagine", types.RelayFormatOpenAI},
		{"unstamped: midjourney mode shadowing /v1", http.MethodPost, "/v1/mj/submit/imagine", types.RelayFormatOpenAI},
		{"unstamped: suno", http.MethodPost, "/suno/submit/music", types.RelayFormatOpenAI},
		{"unstamped: music", http.MethodPost, "/v1/audio/music", types.RelayFormatOpenAI},
		{"unstamped: generic tasks", http.MethodPost, "/v1/tasks/kling", types.RelayFormatOpenAI},
		{"unstamped: video generations", http.MethodPost, "/v1/video/generations", types.RelayFormatOpenAI},
		{"unstamped: kling", http.MethodPost, "/kling/v1/videos/text2video", types.RelayFormatOpenAI},
		{"unstamped: jimeng", http.MethodPost, "/jimeng/", types.RelayFormatOpenAI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relayFormatForPath(tc.method, tc.path); got != tc.want {
				t.Errorf("relayFormatForPath(%s %s) = %q, want %q", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// l5MountGeminiV1Router mirrors relay-router.go's relayV1Router layout for the
// two /v1 routes that speak Gemini: StampRelayFormat is the first Use(), and
// the rejecting middleware (here a stand-in for TokenAuth/PoolBalanceCheck,
// rejecting through the same abortWithOpenAiMessage helper they use) comes
// after it.
func l5MountGeminiV1Router() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	terminal := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) }

	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat(), func(c *gin.Context) {
		abortWithOpenAiMessage(c, http.StatusUnauthorized, "no valid api key", string(types.ErrorCodeAccessDenied))
	})
	v1.POST("/engines/:model/embeddings", terminal)
	v1.POST("/models/*path", terminal)
	v1.POST("/chat/completions", terminal)

	return r
}

// TestRenderRejection_GeminiV1Routes_UseGeminiEnvelope drives the two routes
// end to end through gin: a middleware rejection on a route whose relay stage
// speaks Gemini must render the Gemini envelope {"error":{"code":...,
// "message":...,"status":...}}, not the OpenAI one.
func TestRenderRejection_GeminiV1Routes_UseGeminiEnvelope(t *testing.T) {
	r := l5MountGeminiV1Router()

	for _, path := range []string{
		"/v1/engines/text-embedding-004/embeddings",
		"/v1/models/gemini-2.0-flash:generateContent",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.HasPrefix(body, `{"error":{"code":401`) {
				t.Errorf(`body = %s, want the Gemini envelope {"error":{"code":401,...`, body)
			}
			if !strings.Contains(body, `"status":"UNAUTHENTICATED"`) {
				t.Errorf(`body = %s, want the google.rpc status name UNAUTHENTICATED`, body)
			}
		})
	}

	// Non-regression: the OpenAI sibling on the same group keeps the OpenAI
	// envelope.
	t.Run("/v1/chat/completions stays OpenAI", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		body := w.Body.String()
		if strings.HasPrefix(body, `{"error":{"code":401`) {
			t.Errorf("body = %s, must not be the Gemini envelope", body)
		}
		if !strings.Contains(body, `"message":`) || !strings.Contains(body, `"type":`) {
			t.Errorf(`body = %s, want the OpenAI envelope {"error":{"message":...,"type":...}}`, body)
		}
	})
}
