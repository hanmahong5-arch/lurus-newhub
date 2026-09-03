package handler

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"

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

// Fault modes. The set is deliberately small: each one exists because some
// guarantee cannot be demonstrated without it.
const (
	// FaultModeMidStreamAbort accepts the request, emits a few well-formed SSE
	// frames, then closes without a terminator. This is the only way to reach
	// the incomplete-stream path and the failover-suppressed counter.
	FaultModeMidStreamAbort = "mid_stream_abort"
	// FaultModeSlowHeaders holds the response open before the first byte,
	// exercising the relay's own idle timeout (→ upstream_timeout, 504).
	FaultModeSlowHeaders = "slow_headers"
	// FaultModeHTTP500 is the plain upstream fault that drives the circuit
	// breaker toward Open.
	FaultModeHTTP500 = "http_500"
	// FaultModeRateLimit429 exercises the rate-limit classification and the
	// Retry-After path.
	FaultModeRateLimit429 = "rate_limit_429"
	// FaultModeInsufficientBalance reproduces an unpaid provider account: a 402
	// that must classify as upstream_insufficient_balance rather than
	// upstream_4xx. UAT's real DeepSeek account is already in this state, which
	// is what made the classification urgent.
	FaultModeInsufficientBalance = "upstream_insufficient_balance"
)

// FaultSimModes is the supported set, exported so the wiring test can assert
// every mode is reachable rather than trusting a hand-kept list.
var FaultSimModes = []string{
	FaultModeMidStreamAbort,
	FaultModeSlowHeaders,
	FaultModeHTTP500,
	FaultModeRateLimit429,
	FaultModeInsufficientBalance,
}

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

	switch mode {
	case FaultModeHTTP500:
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "simulated upstream failure", "type": "server_error"},
		})

	case FaultModeRateLimit429:
		c.Header("Retry-After", "30")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{"message": "simulated rate limit", "type": "rate_limit_error"},
		})

	case FaultModeInsufficientBalance:
		// DeepSeek's real shape for an exhausted account.
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error": gin.H{"message": "Insufficient Balance", "type": "insufficient_quota"},
		})

	case FaultModeSlowHeaders:
		// Sleep before writing anything, so the caller is still waiting on the
		// first byte. Bounded, and abandoned early if the caller hangs up —
		// this must never outlive the request it belongs to.
		delay := 30 * time.Second
		if v := c.Query("delay_ms"); v != "" {
			if ms, err := strconv.Atoi(v); err == nil && ms >= 0 && ms <= 120000 {
				delay = time.Duration(ms) * time.Millisecond
			}
		}
		select {
		case <-time.After(delay):
			c.JSON(http.StatusOK, gin.H{
				"id":      "faultsim-slow",
				"object":  "chat.completion",
				"choices": []gin.H{{"index": 0, "message": gin.H{"role": "assistant", "content": "late"}}},
			})
		case <-c.Request.Context().Done():
		}

	case FaultModeMidStreamAbort:
		// The whole point of this handler. Emit valid SSE frames, flush them so
		// they genuinely leave the process, then return WITHOUT the terminator
		// and without a usage frame — exactly the shape of an upstream that
		// died mid-answer.
		helper.SetEventStreamHeaders(c)
		frames := 3
		if v := c.Query("frames"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 100 {
				frames = n
			}
		}
		for i := 0; i < frames; i++ {
			chunk := gin.H{
				"id":     "faultsim-abort",
				"object": "chat.completion.chunk",
				"model":  FaultModeMidStreamAbort,
				"choices": []gin.H{{
					"index": 0,
					"delta": gin.H{"role": "assistant", "content": fmt.Sprintf("part-%d ", i)},
				}},
			}
			if err := helper.ObjectData(c, chunk); err != nil {
				return
			}
			if c.Request.Context().Err() != nil {
				return
			}
		}
		// Deliberately no helper.Done(c): no [DONE], no finish_reason. Returning
		// here ends the response body mid-stream.

	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("unknown fault mode %q; supported: %v", mode, FaultSimModes),
				"type":    "faultsim",
			},
		})
	}
}
