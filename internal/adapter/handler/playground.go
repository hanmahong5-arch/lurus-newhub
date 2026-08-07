package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// playgroundUpstreamBaseURL is the base URL for in-process self-HTTP calls
// to /v1/chat/completions during PlaygroundFanOut. Empty = derive from PORT
// env (default 3000). Tests override this with an httptest.Server URL so the
// handler runs hermetically without standing up the full relay stack.
var playgroundUpstreamBaseURL = ""

// playgroundHTTPClient is package-level so tests can swap in a mock
// transport. Default uses a 120s timeout — matches RELAY_TIMEOUT semantics.
var playgroundHTTPClient = &http.Client{Timeout: 120 * time.Second}

func resolvePlaygroundBaseURL() string {
	if playgroundUpstreamBaseURL != "" {
		return playgroundUpstreamBaseURL
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	return "http://127.0.0.1:" + port
}

func Playground(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(),
			})
		}
	}()

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		// 纯权限拒绝：显式给 403，否则 NewError 默认落到 500，客户端和告警
		// 无法把它与真正的服务端故障区分开。
		newAPIError = types.NewErrorWithStatusCode(errors.New("暂不支持使用 access token"), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, nil, nil)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		return
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := repo.GetUserCache(userId)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		return
	}
	repo.UserBaseWriteContext(userCache, c)

	tempToken := &repo.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	_ = middleware.SetupContextForToken(c, tempToken)

	Relay(c, types.RelayFormatOpenAI)
}

// playgroundChatMessage is OpenAI chat message shape — system / user only
// for the playground v1 (no assistant turns, no tool calls).
type playgroundChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// playgroundModelResult is one cell of the fan-out response — what the
// frontend renders into each of the side-by-side columns. ErrorCode +
// ErrorMessage are non-empty exactly when this single model's call failed
// (other columns may still have succeeded — they're independent).
type playgroundModelResult struct {
	Model            string `json:"model"`
	Content          string `json:"content"`
	LatencyMs        int64  `json:"latency_ms"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

// PlaygroundFanOut runs the same (system, user) prompt against N models in
// parallel and returns per-model {content, latency, tokens, error}. Route:
//
//	POST /api/v2/:tenant_slug/playground/run
//	Auth: UserAuth (session)
//	Body: { system, user, models[], params:{temperature, top_p, max_tokens} }
//
// Implementation strategy: self-HTTP loopback. Each goroutine builds an
// OpenAI chat-completions request and POSTs to this same process's
// /v1/chat/completions, using the user's existing token (auto-created if
// absent — matches PlaygroundAuth's UX). This reuses the full relay
// pipeline (TokenAuth → Distribute → channel selection → upstream call →
// quota debit → billing) without re-implementing any of it in-process.
//
// Why self-HTTP rather than calling Relay() directly: Relay assumes a
// real gin.Context tied to a single response writer (streaming SSE writes
// directly to the connection). Constructing N synthetic contexts and
// multiplexing their outputs is fragile; a localhost roundtrip is cheap
// (sub-millisecond TCP) and keeps the relay code path untouched.
//
// Per-column errors do not fail the whole call — a 502 from one upstream
// surfaces as that column's error_code; the other two columns still
// return their content. HTTP status is always 200 for the fan-out as a
// whole (unless the request itself is malformed → 400).
func PlaygroundFanOut(c *gin.Context) {
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

	var req struct {
		System string   `json:"system"`
		User   string   `json:"user" binding:"required"`
		Models []string `json:"models" binding:"required,min=1,max=5"`
		Params struct {
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

	// Resolve the user's relay token. Mirrors PlaygroundAuth so the web
	// UI never asks the user to manually pick / create a token before
	// using the playground.
	tokenKey, err := resolvePlaygroundToken(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to resolve relay token: " + err.Error(),
			"error_code": "TOKEN_RESOLUTION_FAILED",
		})
		return
	}

	baseURL := resolvePlaygroundBaseURL()
	messages := []playgroundChatMessage{}
	if req.System != "" {
		messages = append(messages, playgroundChatMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, playgroundChatMessage{Role: "user", Content: req.User})

	results := make([]playgroundModelResult, len(req.Models))
	var wg sync.WaitGroup
	for i, modelName := range req.Models {
		wg.Add(1)
		go func(idx int, model string) {
			defer wg.Done()
			results[idx] = runOnePlaygroundModel(
				c.Request.Context(), baseURL, tokenKey, model, messages, req.Params,
			)
		}(i, modelName)
	}
	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"items": results},
	})
}

// resolvePlaygroundToken returns the raw Token.Key for the user's first
// enabled token, auto-creating one if absent (matches PlaygroundAuth).
// Returns the literal key (no "sk-" prefix) — the caller adds the prefix
// when building the Authorization header.
func resolvePlaygroundToken(userID int) (string, error) {
	tokens, err := repo.GetAllUserTokens(userID, 0, 1)
	if err != nil {
		return "", fmt.Errorf("list tokens: %w", err)
	}
	if len(tokens) > 0 && tokens[0].Status == common.TokenStatusEnabled {
		return tokens[0].Key, nil
	}
	t, err := repo.AutoCreateDefaultToken(userID)
	if err != nil {
		return "", fmt.Errorf("auto-create: %w", err)
	}
	return t.Key, nil
}

// runOnePlaygroundModel does the self-HTTP POST + response parse for a
// single model. Errors are bundled into the result struct, never returned
// — the fan-out aggregator wants per-column status, not first-failure-
// aborts-everything.
func runOnePlaygroundModel(
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
		// Cap echoed body size to avoid stuffing megabytes of upstream HTML
		// (e.g. NGINX 502 page) into the response.
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
