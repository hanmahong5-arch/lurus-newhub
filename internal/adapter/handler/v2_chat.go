package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// ChatSend runs a single-model multi-turn chat completion via in-process
// self-HTTP loopback to /v1/chat/completions. The conversation history is
// passed in full from the client on every call — this handler itself has
// no server-side conversation store and neither reads nor writes the
// chat_sessions/chat_messages tables (migration 038,
// v2_chat_session.go's GET/POST/PATCH/DELETE
// /api/v2/:tenant_slug/chat/sessions[/:id]): those tables are a client-
// driven SAVE of a conversation this handler already returned, not
// something this handler consults on its own hot path. A caller that wants
// the turn persisted calls the sessions endpoints separately after this one
// returns.
//
// Route: POST /api/v2/:tenant_slug/chat/send
// Auth:  UserAuth (session)
// Body:  { model, messages: [{role, content}, ...], params? }
//
// non-stream only — SSE-through-loopback would require a second HTTP hop
// that re-multiplexes upstream chunks (this process would have to open its
// own SSE connection to itself, decode each vendor chunk, and re-encode it
// onto the client's connection); that hop is not built, so stream:true is
// not accepted here. This is unrelated to the persistence question above —
// streaming stayed out of scope independently of whether sessions exist.
func ChatSend(c *gin.Context) {
	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	slug := c.Param("tenant_slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	var req struct {
		Model    string                  `json:"model" binding:"required"`
		Messages []playgroundChatMessage `json:"messages" binding:"required,min=1"`
		Params   struct {
			Temperature *float64 `json:"temperature"`
			TopP        *float64 `json:"top_p"`
			MaxTokens   *int     `json:"max_tokens"`
		} `json:"params"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid request: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}

	tokenKey, err := resolvePlaygroundToken(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to resolve relay token: " + err.Error(),
			"error_code": "TOKEN_RESOLUTION_FAILED",
		})
		return
	}

	result := runChatTurn(c.Request.Context(), resolvePlaygroundBaseURL(), tokenKey, req.Model, req.Messages, req.Params)

	if result.ErrorCode != "" {
		c.JSON(http.StatusBadGateway, gin.H{
			"success":    false,
			"message":    result.ErrorMessage,
			"error_code": result.ErrorCode,
			"latency_ms": result.LatencyMs,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"message": gin.H{
				"role":    "assistant",
				"content": result.Content,
			},
			"usage": gin.H{
				"prompt_tokens":     result.PromptTokens,
				"completion_tokens": result.CompletionTokens,
			},
			"latency_ms": result.LatencyMs,
		},
	})
}

// runChatTurn is the single-model analogue of runOnePlaygroundModel —
// same self-HTTP loopback pattern, but the full message history is
// passed through verbatim (Playground only sends one user turn).
func runChatTurn(
	ctx context.Context,
	baseURL, tokenKey, model string,
	messages []playgroundChatMessage,
	params struct {
		Temperature *float64 `json:"temperature"`
		TopP        *float64 `json:"top_p"`
		MaxTokens   *int     `json:"max_tokens"`
	},
) playgroundModelResult {
	r := playgroundModelResult{Model: model}
	start := time.Now()

	body := map[string]interface{}{
		"model":    model,
		"stream":   false,
		"messages": messages,
	}
	if params.Temperature != nil {
		body["temperature"] = *params.Temperature
	}
	if params.TopP != nil {
		body["top_p"] = *params.TopP
	}
	if params.MaxTokens != nil {
		body["max_tokens"] = *params.MaxTokens
	}

	data, err := json.Marshal(body)
	if err != nil {
		r.ErrorCode = "MARSHAL_FAILED"
		r.ErrorMessage = err.Error()
		r.LatencyMs = time.Since(start).Milliseconds()
		return r
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(data),
	)
	if err != nil {
		r.ErrorCode = "BUILD_REQUEST_FAILED"
		r.ErrorMessage = err.Error()
		r.LatencyMs = time.Since(start).Milliseconds()
		return r
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer sk-"+tokenKey)

	resp, err := playgroundHTTPClient.Do(httpReq)
	r.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		r.ErrorCode = "RELAY_FAILED"
		r.ErrorMessage = err.Error()
		return r
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &openaiResp); err != nil {
		r.ErrorCode = "PARSE_FAILED"
		preview := string(respBody)
		if len(preview) > 500 {
			preview = preview[:500] + "…(truncated)"
		}
		r.ErrorMessage = preview
		return r
	}

	if openaiResp.Error != nil {
		r.ErrorCode = "UPSTREAM_ERROR"
		if openaiResp.Error.Code != "" {
			r.ErrorCode = openaiResp.Error.Code
		}
		r.ErrorMessage = openaiResp.Error.Message
		return r
	}

	if len(openaiResp.Choices) > 0 {
		r.Content = openaiResp.Choices[0].Message.Content
	}
	r.PromptTokens = openaiResp.Usage.PromptTokens
	r.CompletionTokens = openaiResp.Usage.CompletionTokens
	return r
}
