package middleware

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// StampRelayFormat records which wire shape the inbound request speaks
// (constant.ContextKeyRelayFormat), derived from the request method and path
// alone — at rejection time (a bad key, an exhausted pool, a tripped rate limit)
// none of the auth-derived signals that would normally pick a format exist
// yet. Every relay group that can 401/402/429 a caller ahead of
// handler.Relay (which is the only place that otherwise learns the format,
// too late for a middleware-stage rejection to consult it) must mount this
// as its FIRST Use() — gin snapshots a group's middleware chain at Group()
// registration time, so the stamp has to precede every rejecting middleware
// already registered on that group.
func StampRelayFormat() gin.HandlerFunc {
	return func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyRelayFormat,
			relayFormatForPath(c.Request.Method, c.Request.URL.Path))
		c.Next()
	}
}

// relayFormatForPath mirrors the format each route in relay-router.go
// passes to handler.Relay: /v1/messages(/count_tokens) speaks Claude's wire,
// the /v1beta/models shapes and the two /v1 routes relay-router.go hands to
// RelayFormatGemini (POST /v1/engines/:model/embeddings and POST
// /v1/models/*path) speak Gemini's, everything else speaks OpenAI's.
//
// The method is load-bearing on the /v1/models prefix and nowhere else:
// POST /v1/models/* is the native Gemini relay route, while GET /v1/models
// and GET /v1/models/:model are OpenAI's model listing/retrieval and
// DELETE /v1/models/:model is a not-implemented stub — all three must keep
// the OpenAI envelope.
//
// BLIND SPOT: this function sees the method and the URL path, nothing else.
// It cannot see headers, query or body, so a route that picks its dialect
// from a header — modelsRouter's GET /v1/models dispatches to the Gemini or
// Anthropic model listing on x-goog-api-key / anthropic-version — still gets
// the OpenAI envelope here. It also does not know which routes exist or
// which groups mount StampRelayFormat at all; a group that never mounts it
// falls back to the OpenAI shape in renderRejection regardless of what this
// function would have answered. In particular it cannot see that a path is
// served by a group with a WILDCARD segment (relay-router.go:247 registers
// the Midjourney group as "/:mode/mj", so the caller chooses :mode): the
// rules below have to be written narrowly enough that no caller-chosen
// segment can steer a route into another vendor's envelope — see
// isV1betaGeminiRelayPath.
func relayFormatForPath(method, path string) types.RelayFormat {
	switch {
	case path == "/v1/messages" || path == "/v1/messages/count_tokens":
		return types.RelayFormatClaude
	case isV1betaGeminiRelayPath(path):
		return types.RelayFormatGemini
	case method == http.MethodPost && isV1GeminiRelayPath(path):
		return types.RelayFormatGemini
	default:
		return types.RelayFormatOpenAI
	}
}

// isV1betaGeminiRelayPath reports whether path is one of the /v1beta shapes
// relay-router.go registers on a group that mounts StampRelayFormat: GET
// /v1beta/models (geminiRouter, the Gemini model listing, relay-router.go:42)
// and POST /v1beta/models/*path (relayGeminiRouter's geminiHTTPRouter,
// relay-router.go:292/310 — the native Gemini relay, whose paths look like
// /v1beta/models/<model>:<action>). Those are the only /v1beta registrations
// in the tree; the method is not consulted because both are Gemini's.
//
// Deliberately NOT a bare "/v1beta/" prefix, which is what this used to be:
// the Midjourney group is registered as "/:mode/mj", so :mode is a segment
// the CALLER picks, and gin backtracks past the /v1beta group to it. Measured
// through the real SetRelayRouter, POST /v1beta/mj/submit/imagine is a live
// Midjourney route answering 401 (not 404) — and the prefix rule called it
// "gemini". That group does not mount the stamp today, so the wrong answer
// stayed unreachable, but it was one Use() away from putting a Gemini
// envelope on a Midjourney rejection.
//
// /v1beta/openai/models (geminiCompatibleRouter, the OpenAI-compatible
// listing) keeps falling to the OpenAI default: it is not under
// /v1beta/models, so it needs no special case any more.
func isV1betaGeminiRelayPath(path string) bool {
	return path == "/v1beta/models" || strings.HasPrefix(path, "/v1beta/models/")
}

// isV1GeminiRelayPath reports whether path is one of the two /v1 relay routes
// registered with types.RelayFormatGemini in relay-router.go. Callers must
// already have checked the method (see relayFormatForPath) — this function is
// path-shape only.
func isV1GeminiRelayPath(path string) bool {
	// POST /v1/models/*path, i.e. /v1/models/<model>:<action>.
	//
	// The bare "/v1/models" equality is DEFENSIVE ONLY, not a live route:
	// measured through the real SetRelayRouter, POST /v1/models answers
	// "404 page not found" — gin's catch-all does not match its own bare
	// prefix, and modelsRouter registers /v1/models for GET only. It is kept
	// so the answer stays stable if a bare-prefix route or a path normaliser
	// ever delivers that exact path, and it costs nothing: with no route
	// there is no stamp and no rejection to render.
	if path == "/v1/models" || strings.HasPrefix(path, "/v1/models/") {
		return true
	}
	// POST /v1/engines/:model/embeddings — :model is exactly one segment, so
	// neither a shorter nor a deeper path matches.
	if rest, ok := strings.CutPrefix(path, "/v1/engines/"); ok {
		model, tail, found := strings.Cut(rest, "/")
		return found && model != "" && tail == "embeddings"
	}
	return false
}

// renderRejection writes a middleware-stage rejection (apiErr) in the
// caller's own wire shape, mirroring the per-wire switch handler.Relay's
// deferred error handler runs, so a 401 from TokenAuth or a 402
// from PoolBalanceCheck looks identical, to the client's SDK, to a
// relay-stage failure. Falls back to the OpenAI shape (today's behaviour)
// when no format was stamped — every caller of this outside a stamped
// group (e.g. a unit test that exercises a middleware directly) keeps
// answering exactly as before.
//
// extra carries wire-specific fields (e.g. pool_balance_check.go's
// tenant_id) that don't fit types.OpenAIError's shape; merged into the
// OpenAI-wire error object only, which is where the existing locks on
// those fields live — the Claude/Gemini envelopes have no room for them.
func renderRejection(c *gin.Context, apiErr *types.NewAPIError, extra ...gin.H) {
	format, _ := common.GetContextKeyType[types.RelayFormat](c, constant.ContextKeyRelayFormat)
	switch format {
	case types.RelayFormatClaude:
		c.JSON(apiErr.StatusCode, gin.H{
			"type":  "error",
			"error": apiErr.ToClaudeError(),
		})
	case types.RelayFormatGemini:
		c.JSON(apiErr.StatusCode, apiErr.ToGeminiError())
	default:
		oaiErr := apiErr.ToOpenAIError()
		if len(extra) == 0 {
			c.JSON(apiErr.StatusCode, gin.H{"error": oaiErr})
			return
		}
		body := gin.H{
			"message": oaiErr.Message,
			"type":    oaiErr.Type,
			"param":   oaiErr.Param,
			"code":    oaiErr.Code,
		}
		if len(oaiErr.Metadata) > 0 {
			body["metadata"] = oaiErr.Metadata
		}
		for _, e := range extra {
			for k, v := range e {
				body[k] = v
			}
		}
		c.JSON(apiErr.StatusCode, gin.H{"error": body})
	}
}
