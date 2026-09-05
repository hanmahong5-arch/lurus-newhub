package middleware

import (
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// StampRelayFormat records which wire shape the inbound request speaks
// (constant.ContextKeyRelayFormat), derived from the request path alone —
// at rejection time (a bad key, an exhausted pool, a tripped rate limit)
// none of the auth-derived signals that would normally pick a format exist
// yet. Every relay group that can 401/402/429 a caller ahead of
// handler.Relay (which is the only place that otherwise learns the format,
// too late for a middleware-stage rejection to consult it) must mount this
// as its FIRST Use() — gin snapshots a group's middleware chain at Group()
// registration time, so the stamp has to precede every rejecting middleware
// already registered on that group.
func StampRelayFormat() gin.HandlerFunc {
	return func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyRelayFormat, relayFormatForPath(c.Request.URL.Path))
		c.Next()
	}
}

// relayFormatForPath mirrors the format each route in relay-router.go
// passes to handler.Relay: /v1/messages(/count_tokens) speaks Claude's
// wire, anything under /v1beta/ other than the OpenAI-compatible
// /v1beta/openai/ subtree speaks Gemini's, everything else speaks OpenAI's.
func relayFormatForPath(path string) types.RelayFormat {
	switch {
	case path == "/v1/messages" || path == "/v1/messages/count_tokens":
		return types.RelayFormatClaude
	case strings.HasPrefix(path, "/v1beta/") && !strings.HasPrefix(path, "/v1beta/openai/"):
		return types.RelayFormatGemini
	default:
		return types.RelayFormatOpenAI
	}
}

// renderRejection writes a middleware-stage rejection (apiErr) in the
// caller's own wire shape, mirroring the switch handler.Relay's deferred
// error handler runs (relay.go:213-233) so a 401 from TokenAuth or a 402
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
