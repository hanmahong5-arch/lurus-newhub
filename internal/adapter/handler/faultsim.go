package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/testkit/fakeupstream"

	"github.com/gin-gonic/gin"
)

// A controllable upstream, for proving failure handling that cannot otherwise
// be proven.
//
// Three guarantees currently rest on unit tests alone: mid-stream abandonment,
// circuit-breaker transitions, and failover suppression. The planning notes for
// the last one say it plainly — "Live proof needs an upstream that can be made
// to die mid-stream; not reproducible on UAT". That is structurally true, not a
// gap in effort: relay_failover_suppressed_total{reason="stream_already_started"}
// can only be incremented by a real upstream that accepts a request, emits some
// frames, and then stops. An in-process test cannot produce that, because the
// thing being tested is what our HTTP client does with a half-delivered
// response body.
//
// This is that upstream. It is an ordinary OpenAI-compatible endpoint hosted by
// newhub itself, which a UAT channel can point at.
//
// SAFETY. Three independent reasons this cannot affect production:
//
//  1. The routes are registered only when FAULTSIM_TOKEN is non-empty — the
//     same construction as the e2e bridge (BridgeEnabled). In production the
//     routes do not exist, so there is nothing to authenticate, rate-limit or
//     get wrong. TestFaultSimRouteAbsentByDefault holds this.
//  2. Every request must present the token. An operator who sets the env on
//     the wrong instance still has not opened an anonymous endpoint.
//  3. The UAT channel that uses it points at http://127.0.0.1:3000. That
//     address is deliberate: deploy/k8s/r6-uat/netpol-egress.yaml excludes all
//     of RFC1918, so a fault-injector deployed as a separate Pod would be
//     unreachable at the network layer. Loopback is not subject to the
//     NetworkPolicy, and UAT runs a single replica, so the request lands in
//     the same process that served it and circuit-breaker state is
//     unambiguous. Zero new images, zero new manifests.
//
// It writes no logs, touches no quota and reaches no provider.
func FaultSimEnabled() bool {
	return os.Getenv("FAULTSIM_TOKEN") != ""
}

// Fault modes. They are internal/testkit/fakeupstream's: this simulator and
// the fake vendor the local acceptance stack runs are one implementation, so a
// fault proven locally is the same fault UAT serves. The first five names
// predate the shared package and are kept so seeded UAT channels keep working:
//
//   - mid_stream_abort: a few well-formed SSE frames, then the body ends with
//     no terminator and no usage frame — the only way to reach the
//     incomplete-stream path and the failover-suppressed counter.
//   - slow_headers: hold before the first byte (the relay's idle timeout →
//     upstream_timeout, 504).
//   - http_500: the plain fault that drives the circuit breaker toward Open.
//   - rate_limit_429: the rate-limit classification and Retry-After.
//   - upstream_insufficient_balance: an unpaid provider account's 402, which
//     must classify as upstream_insufficient_balance, not upstream_4xx.
//   - http_401, disconnect_before_first_byte: a rejected channel key, and a
//     connection closed before any status line.
const (
	FaultModeMidStreamAbort      = fakeupstream.FaultMidStreamAbort
	FaultModeSlowHeaders         = fakeupstream.FaultSlowHeaders
	FaultModeHTTP500             = fakeupstream.FaultHTTP500
	FaultModeRateLimit429        = fakeupstream.FaultRateLimit429
	FaultModeInsufficientBalance = fakeupstream.FaultInsufficientBalance
)

// FaultSimModes is the supported set, exported so the wiring test can assert
// every mode is reachable rather than trusting a hand-kept list.
var FaultSimModes = fakeupstream.FaultModes

func faultSimAuthorized(c *gin.Context) bool {
	want := os.Getenv("FAULTSIM_TOKEN")
	if want == "" {
		return false
	}
	got := c.GetHeader("X-Faultsim-Token")
	if got == "" {
		// Channels send their key as a bearer token; accept that shape too so a
		// channel can be seeded without a custom header.
		if auth := c.GetHeader("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
			got = auth[7:]
		}
	}
	if got == "" {
		// The Anthropic adaptor sends the channel key as x-api-key.
		got = c.GetHeader("x-api-key")
	}
	return got == want
}

// Success mode. Business tests (billing, quota, logs, dashboards) need an
// upstream that ANSWERS, which a fault-only simulator cannot be. A model name
// that is exactly "ok" or starts with "ok-" (ok, ok-chat, ok-msgs, ...)
// returns a well-formed success on the wire the request path selects:
//
//   - /faultsim/v1/chat/completions: OpenAI chat (JSON, or SSE when stream=true
//     with usage in the last data frame followed by [DONE])
//   - /faultsim/v1/responses:        OpenAI Responses
//   - /faultsim/v1/messages:         Anthropic messages (full event sequence)
//
// The wire builders are internal/testkit/fakeupstream's — the same ones the
// local acceptance stack serves — so there is no second copy to drift. Usage
// is fixed and predictable (prompt 1000 / completion 500, fakeupstream's
// DefaultUsage) and can be overridden per request with
// `X-Faultsim-Usage: "in,out"` so a business test can compute the expected
// charge. The reply is a fixed short Chinese sentence.
const faultSimOKReply = "你好，这是模拟上游的固定回复。"

// FaultSimUsageHeader overrides the reported usage: "in,out".
const FaultSimUsageHeader = "X-Faultsim-Usage"

// IsFaultSimOKModel reports whether model selects success mode.
func IsFaultSimOKModel(model string) bool {
	return model == "ok" || strings.HasPrefix(model, "ok-")
}

func faultSimUsage(c *gin.Context) (fakeupstream.Usage, error) {
	u := fakeupstream.DefaultUsage
	raw := strings.TrimSpace(c.GetHeader(FaultSimUsageHeader))
	if raw == "" {
		return u, nil
	}
	in, out, ok := strings.Cut(raw, ",")
	pi, err1 := strconv.Atoi(strings.TrimSpace(in))
	po, err2 := strconv.Atoi(strings.TrimSpace(out))
	if !ok || err1 != nil || err2 != nil || pi < 0 || po < 0 {
		return u, fmt.Errorf("%s must be \"in,out\" with non-negative integers, got %q", FaultSimUsageHeader, raw)
	}
	return fakeupstream.Usage{PromptTokens: pi, CompletionTokens: po}, nil
}

// serveFaultSimOK delegates a success answer to the shared fake vendor.
// vendorPath is the fakeupstream route for the wire.
func serveFaultSimOK(c *gin.Context, vendorPath string, body map[string]any) {
	usage, err := faultSimUsage(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error(), "type": "faultsim"}})
		return
	}
	// The OpenAI wire streams usage only when asked; success mode always
	// reports it so the last frame is billable.
	if stream, _ := body["stream"].(bool); stream && vendorPath == "/v1/chat/completions" {
		opts, _ := body["stream_options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		opts["include_usage"] = true
		body["stream_options"] = opts
	}
	raw, _ := json.Marshal(body)
	srv := fakeupstream.New(fakeupstream.Config{Usage: usage, Reply: faultSimOKReply})
	req := c.Request.Clone(c.Request.Context())
	req.URL.Path = vendorPath
	req.URL.RawQuery = ""
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	srv.Handler().ServeHTTP(c.Writer, req)
}

func faultSimServe(c *gin.Context, wire, vendorPath string) {
	if !faultSimAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "fault simulator token required", "type": "faultsim"},
		})
		return
	}

	// A malformed body is not interesting here; default to the plain 500 mode
	// rather than adding a failure shape nobody asked for.
	body := map[string]any{}
	_ = c.ShouldBindJSON(&body)
	model, _ := body["model"].(string)

	mode := model
	if q := c.Query("mode"); q != "" {
		mode = q
	}

	if IsFaultSimOKModel(mode) {
		serveFaultSimOK(c, vendorPath, body)
		return
	}
	if !fakeupstream.IsFaultMode(mode) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("unknown fault mode %q; supported: %v, or ok / ok-* for success", mode, FaultSimModes),
				"type":    "faultsim",
			},
		})
		return
	}
	fakeupstream.WriteFault(c.Writer, c.Request, wire, mode, fakeupstream.FaultParamsFromQuery(c.Request))
}

// FaultSimChatCompletions serves POST /faultsim/v1/chat/completions.
//
// The mode is chosen by the `model` field of the request body, so a channel
// needs no special configuration: seed a UAT channel whose model list is the
// mode names and the mode is selected by asking for that "model".
func FaultSimChatCompletions(c *gin.Context) {
	faultSimServe(c, fakeupstream.WireOpenAIChat, "/v1/chat/completions")
}

// FaultSimResponses serves POST /faultsim/v1/responses (OpenAI Responses).
func FaultSimResponses(c *gin.Context) {
	faultSimServe(c, fakeupstream.WireOpenAIResponses, "/v1/responses")
}

// FaultSimSystemOne serves POST /faultsim/v1/systemone (TypeSafe wire).
//
// Success mode (model "ok" / "ok-*", e.g. "ok-decision") answers every choice
// question with the fixed distribution fakeupstream.serveSystemOne derives
// from the criteria text, so a UAT channel of type TypeSafe / System One
// compatible pointed at this process can drive decision routing end to end
// with no vendor key (see doc/decisions/2026-10-10-decision-model-routing.md).
func FaultSimSystemOne(c *gin.Context) {
	faultSimServe(c, fakeupstream.WireSystemOne, "/v1/systemone")
}

// FaultSimMessages serves POST /faultsim/v1/messages (Anthropic messages).
func FaultSimMessages(c *gin.Context) {
	faultSimServe(c, fakeupstream.WireAnthropic, "/v1/messages")
}

// --- Task-vendor fault simulator (cycle-8 L8) ---
//
// FaultSimTaskSubmit/FaultSimTaskFetch imitate the Suno wire ONE compiled
// adaptor already speaks (internal/adapter/provider/task/suno) — chosen
// because it is the async-task adaptor with the simplest submit body (no
// multipart) and because its batch fetch (task.go's updateSunoTaskAll,
// driven every 15s by UpdateTaskBulkWithContext) needs only one HTTP round
// trip regardless of how many tasks are pending. This does NOT add a new
// TaskPlatform or adaptor — GetTaskAdaptor(constant.TaskPlatformSuno) is
// unchanged; a UAT channel of type ChannelTypeSunoAPI simply points its
// base_url at this process's own loopback address (see the SAFETY doc
// comment above) instead of the real Suno-compatible vendor.
//
// Same two-artefact shape on every fetch: a data: URL text/plain payload
// and a data: URL image — proving the round trip (and, once L9 lands, the
// artefact-listing/proxy generalisation) needs no vendor key on UAT.
const (
	faultSimTaskArtefactText = "data:text/plain;base64,VGFzayBjb21wbGV0ZWQgYnkgdGhlIGZhdWx0IHNpbXVsYXRvcg=="
	// 1x1 transparent PNG.
	faultSimTaskArtefactImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
)

// FaultSimTaskSubmit serves POST /faultsim/suno/submit/:action — the same
// path shape suno.TaskAdaptor.BuildRequestURL constructs
// (baseURL + "/suno/submit/" + action). Always accepts and returns a fresh
// task id in the TaskResponse[string] envelope suno.TaskAdaptor.DoResponse
// expects.
func FaultSimTaskSubmit(c *gin.Context) {
	if !faultSimAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "fault simulator token required", "type": "faultsim"},
		})
		return
	}
	taskID := "faultsim-task-" + common.GetRandomString(12)
	c.JSON(http.StatusOK, gin.H{
		"code":    "success",
		"message": "",
		"data":    taskID,
	})
}

// FaultSimTaskFetch serves POST /faultsim/suno/fetch, the batch status poll
// task.go's updateSunoTaskAll drives with body {"ids": [...]}. Every id
// requested is answered SUCCESS immediately (no queued/in_progress
// simulation — the point is a fast, deterministic UAT round trip, not
// reproducing real generation latency), each with the two data: URL
// artefacts described above.
func FaultSimTaskFetch(c *gin.Context) {
	if !faultSimAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "fault simulator token required", "type": "faultsim"},
		})
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	// A malformed/empty body just yields zero results — nothing to fault on.
	_ = c.ShouldBindJSON(&req)

	artefacts, _ := json.Marshal(gin.H{
		"url":       faultSimTaskArtefactText,
		"image_url": faultSimTaskArtefactImage,
	})
	now := time.Now().Unix()
	items := make([]gin.H, 0, len(req.IDs))
	for _, id := range req.IDs {
		items = append(items, gin.H{
			"task_id":     id,
			"status":      "SUCCESS",
			"fail_reason": "",
			"submit_time": now,
			"start_time":  now,
			"finish_time": now,
			"data":        json.RawMessage(artefacts),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    "success",
		"message": "",
		"data":    items,
	})
}
