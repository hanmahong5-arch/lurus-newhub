package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	return got == want
}

// FaultSimChatCompletions serves POST /faultsim/v1/chat/completions.
//
// The mode is chosen by the `model` field of the request body, so a channel
// needs no special configuration: seed a UAT channel whose model list is the
// mode names and the mode is selected by asking for that "model".
func FaultSimChatCompletions(c *gin.Context) {
	if !faultSimAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "fault simulator token required", "type": "faultsim"},
		})
		return
	}

	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	// A malformed body is not interesting here; default to the plain 500 mode
	// rather than adding a failure shape nobody asked for.
	_ = c.ShouldBindJSON(&req)

	mode := req.Model
	if q := c.Query("mode"); q != "" {
		mode = q
	}

	if !fakeupstream.IsFaultMode(mode) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("unknown fault mode %q; supported: %v", mode, FaultSimModes),
				"type":    "faultsim",
			},
		})
		return
	}
	fakeupstream.WriteFault(c.Writer, c.Request, fakeupstream.WireOpenAIChat, mode, fakeupstream.FaultParamsFromQuery(c.Request))
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
